import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import api from '../api/client'
import './AdminOverviewPage.css'

/*
 * AdminOverviewPage — /admin/overview (v2)
 *
 * Re-shape of the system-health panorama. Powered by GET /admin/system-health,
 * which already returns infra probes, observability probes, workers/streams
 * rollups, data-pipeline counters, and a classified "attention" list.
 *
 * Two compositions selected by `data.overall`:
 *
 *   nominal (healthy)
 *     ┌──────── PulseHero (~32vh) ──────────────────────┐
 *     │ One living signal across the full width.        │
 *     │ Pink trace = system breathing. Tick on poll.    │
 *     └─────────────────────────────────────────────────┘
 *     ┌─ Workers ─┬─ Streams ─┬─ Pipeline ─┐
 *     │   type-led KPIs, no card chrome    │
 *     └────────────┴───────────┴────────────┘
 *     ┌──── Status strip (infra · obs pills) ───────────┐
 *     │ inline horizontal status — not a card grid      │
 *     └─────────────────────────────────────────────────┘
 *     "Nada exigindo atenção" — educational empty state
 *
 *   incident (degraded | critical)
 *     ┌──────── PulseHero (~12vh strip) ────────────────┐
 *     └─────────────────────────────────────────────────┘
 *     ┌────────── Attention (2/3) ─┬─ Status (1/3) ─────┐
 *     │ stacked evidence cards     │ KPIs + service     │
 *     │ sorted critical → warning  │ strip in column    │
 *     └────────────────────────────┴────────────────────┘
 *
 * Live behaviour:
 *   - useQuery polls every 10s (same as v1).
 *   - On each successful refetch, two new samples are pushed into a small
 *     ring buffer (system-uptime track + detection-intensity track). The
 *     PulseHero draws those as SVG paths with a pulsing playhead.
 *   - No backend changes. All temporal feel comes from the client-side ring.
 */

// ── Status labels ───────────────────────────────────────────────────────────
const INFRA_LABELS = {
  postgres:      { name: 'Postgres', desc: 'Banco principal',         env: 'DATABASE_URL' },
  nats:          { name: 'NATS',     desc: 'Fila de eventos',         env: 'NATS_URL' },
  redis:         { name: 'Redis',    desc: 'Cache em memória',        env: 'REDIS_URL' },
  minio:         { name: 'MinIO',    desc: 'Storage de evidência',    env: 'S3_ENDPOINT' },
  clap_verifier: { name: 'CLAP',     desc: 'Verificador neural',      env: 'CLAP_VERIFIER_URL' },
}

const OBS_LABELS = {
  prometheus: { name: 'Prometheus', desc: 'Coleta de métricas', env: 'PROMETHEUS_URL' },
  grafana:    { name: 'Grafana',    desc: 'Dashboards',         env: 'GRAFANA_URL' },
  jaeger:     { name: 'Jaeger',     desc: 'Tracing',            env: 'JAEGER_URL' },
}

const OVERALL_META = {
  healthy:  { label: 'Sistema no ar',     short: 'No ar',     tone: 'ok'   },
  degraded: { label: 'Sistema degradado', short: 'Degradado', tone: 'warn' },
  critical: { label: 'Sistema crítico',   short: 'Crítico',   tone: 'crit' },
}

const REASON_LABEL = {
  not_registered: 'Não registrado',
  stalled:        'Travado',
  stream_down:    'Stream fora',
}

// ── Helpers ─────────────────────────────────────────────────────────────────
function fmtRelative(iso) {
  if (!iso) return '—'
  const ms = Date.now() - new Date(iso).getTime()
  if (Number.isNaN(ms) || ms < 0) return 'agora'
  if (ms < 60_000)     return `há ${Math.floor(ms / 1000)}s`
  if (ms < 3_600_000)  return `há ${Math.floor(ms / 60_000)}min`
  if (ms < 86_400_000) return `há ${Math.floor(ms / 3_600_000)}h`
  return `há ${Math.floor(ms / 86_400_000)}d`
}

function fmtLatency(ms) {
  if (ms == null) return null
  if (ms < 1)   return '<1ms'
  if (ms < 100) return `${ms}ms`
  return `${(ms / 1000).toFixed(2)}s`
}

// Pluralize helper kept local — no i18n library on this surface yet.
function plural(n, one, many) {
  return n === 1 ? one : many
}

// Headline sentence for the PulseHero overlay. The wording is intentionally
// concrete: nominal answers "is it alive?", degraded names the deficit,
// critical names the worst offender so the operator's first glance lands on
// the right keyword.
function buildHeadline(data) {
  if (!data) return ''
  const overall = data.overall || 'healthy'
  if (overall === 'critical') {
    const infraDown = Object.entries(data.infrastructure || {})
      .find(([name, s]) => ['postgres','nats','minio'].includes(name) && s?.status === 'down')
    if (infraDown) return `${INFRA_LABELS[infraDown[0]]?.name || infraDown[0]} fora`
    if ((data.workers?.missing ?? 0) > 0) return `${data.workers.missing} worker(s) ausente(s)`
    return 'Componente crítico fora'
  }
  if (overall === 'degraded') {
    const issues = []
    if ((data.workers?.stalled ?? 0) > 0) issues.push(`${data.workers.stalled} travado${data.workers.stalled === 1 ? '' : 's'}`)
    if ((data.streams?.down_now ?? 0) > 0) issues.push(`${data.streams.down_now} fora`)
    if (issues.length === 0) issues.push('serviço opcional fora')
    return issues.join(' · ')
  }
  const live = data.streams?.live_now ?? 0
  const expected = data.streams?.expected_active ?? 0
  if (expected === 0) return 'Sem emissoras monitoradas'
  return `${live} de ${expected} emissora${expected === 1 ? '' : 's'} ao ar`
}

// ── Icons ───────────────────────────────────────────────────────────────────
function CheckIcon()   { return <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2.25" strokeLinecap="round" strokeLinejoin="round"><path d="M3 8.5l3 3 7-7"/></svg> }
function XIcon()       { return <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2.25" strokeLinecap="round"><path d="M4 4l8 8M12 4l-8 8"/></svg> }
function DashIcon()    { return <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2.25" strokeLinecap="round"><path d="M3.5 8h9"/></svg> }
function ReloadIcon()  { return <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round"><path d="M14 8a6 6 0 1 1-1.76-4.24"/><path d="M14 2v4h-4"/></svg> }
function ArrowIcon()   { return <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M5 3l5 5-5 5"/></svg> }
function AlertIcon()   { return <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round"><path d="M8 1.5l7 12.5H1L8 1.5z"/><path d="M8 6.5v3.5"/><circle cx="8" cy="12" r="0.7" fill="currentColor" stroke="none"/></svg> }
function HushIcon()    { return <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round"><path d="M2 8h12"/></svg> }

// ── PulseHero ───────────────────────────────────────────────────────────────
//
// The signature element. A horizontal living signal that takes ~32vh in
// nominal mode and compresses to ~12vh in incident mode. Two overlapping
// SVG tracks:
//
//   - Background track: detection intensity (faint pink wash, low opacity)
//   - Foreground track: live-stream ratio (bold trace, semantic color)
//
// Each successful poll pushes one sample into the ring buffer (max 60).
// The trace re-renders on every render; a CSS-animated playhead pulses at
// the right edge.
//
// In incident mode, the foreground track's "deficit zone" (live ÷ expected
// shortfall) is rendered as a soft warn/danger overlay above the baseline,
// so the dip is *visually* present, not just colored.
function PulseHero({ data, overall, samples, isFetching, lastUpdatedRel, onRefresh, isIncident, headline }) {
  const meta = OVERALL_META[overall] ?? OVERALL_META.healthy
  const N = 60

  // Build SVG path strings. Coordinates run 0..N on x, 0..100 on y. The
  // viewBox stretches via preserveAspectRatio="none" so the path scales to
  // whatever the container actually is on screen.
  const fgPath = useMemo(() => buildSmoothPath(samples.fg, N, 100), [samples.fg])
  const bgPath = useMemo(() => buildSmoothPath(samples.bg, N, 100), [samples.bg])
  const lastFg = samples.fg[samples.fg.length - 1] ?? 50

  return (
    <header
      className={`aov-hero aov-hero--${meta.tone} ${isIncident ? 'aov-hero--compact' : ''}`}
      role="region"
      aria-label="Pulso do sistema"
    >
      <svg
        className="aov-hero-svg"
        viewBox="0 0 60 100"
        preserveAspectRatio="none"
        aria-hidden="true"
      >
        {/* Faint horizontal baseline at 50% */}
        <line x1="0" y1="50" x2="60" y2="50" className="aov-hero-baseline" />
        {/* Background wash — detection intensity */}
        <path d={bgPath} className="aov-hero-bg" fill="none" />
        {/* Foreground trace — live-stream ratio */}
        <path d={fgPath} className="aov-hero-fg" fill="none" />
        {/* Playhead — pulses at the right edge */}
        <line x1="59.5" y1="0" x2="59.5" y2="100" className="aov-hero-playhead" />
        <circle cx="59.5" cy={100 - lastFg} r="0.9" className="aov-hero-playhead-dot" />
      </svg>

      <div className="aov-hero-overlay">
        <div className="aov-hero-overlay-left">
          <div className="aov-hero-lamp" aria-hidden="true">
            <span className="aov-hero-lamp-dot" />
          </div>
          <div className="aov-hero-text">
            <span className="aov-hero-eyebrow">{meta.short.toUpperCase()}</span>
            <h1 className="aov-hero-title">{meta.label}</h1>
            {headline && <p className="aov-hero-headline">{headline}</p>}
          </div>
        </div>

        <div className="aov-hero-overlay-right">
          <div className="aov-hero-updated" aria-live="polite">
            <span className="aov-hero-updated-label">Atualizado</span>
            <span className="aov-hero-updated-value">{lastUpdatedRel}</span>
          </div>
          <button
            type="button"
            className={`aov-refresh ${isFetching ? 'aov-refresh--spin' : ''}`}
            onClick={onRefresh}
            aria-label="Atualizar agora (R)"
            title="Atualizar agora — tecla R"
          >
            <ReloadIcon />
          </button>
        </div>
      </div>

      {/* Bottom legend, only in nominal mode where there's room */}
      {!isIncident && data && (
        <div className="aov-hero-legend">
          <span className="aov-hero-legend-row">
            <span className="aov-hero-legend-swatch aov-hero-legend-swatch--fg" />
            Sinal ao ar — {data.streams?.live_now ?? 0}/{data.streams?.expected_active ?? 0}
          </span>
          <span className="aov-hero-legend-row">
            <span className="aov-hero-legend-swatch aov-hero-legend-swatch--bg" />
            Detecções na última hora — {data.data_pipeline?.detections_1h ?? 0}
          </span>
        </div>
      )}
    </header>
  )
}

// buildSmoothPath turns a numeric series into an SVG cubic-bezier path. We
// use a Catmull-Rom-ish smoothing (averaging neighbours for control points)
// so the trace reads as a wave, not as a step graph. Math kept inline — this
// is only called twice per render.
function buildSmoothPath(samples, N, maxY) {
  if (!samples || samples.length === 0) return ''
  // Right-align the trace so the latest sample sits at x=N.
  const start = N - samples.length
  const pts = samples.map((v, i) => [start + i, maxY - clamp(v, 0, maxY)])
  if (pts.length === 1) {
    const [x, y] = pts[0]
    return `M${x} ${y} L${N} ${y}`
  }
  let d = `M${pts[0][0]} ${pts[0][1]}`
  for (let i = 1; i < pts.length; i++) {
    const [x0, y0] = pts[i - 1]
    const [x1, y1] = pts[i]
    const cx = (x0 + x1) / 2
    d += ` Q ${cx} ${y0} ${cx} ${(y0 + y1) / 2} T ${x1} ${y1}`
  }
  return d
}

function clamp(n, lo, hi) {
  return Math.max(lo, Math.min(hi, n))
}

// ── KpiBlock ────────────────────────────────────────────────────────────────
//
// Type-led, no card chrome in nominal mode. The value is the dominant element
// (Space Grotesk, 56–72px); the label and sublines whisper. Hover reveals an
// "Abrir →" link that routes to the relevant page.
function KpiBlock({ label, value, valueSuffix, tone, sublines, linkTo, linkLabel, navigate, compact }) {
  return (
    <a
      className={`aov-kpi aov-kpi--${tone || 'ok'} ${compact ? 'aov-kpi--compact' : ''}`}
      href={linkTo}
      onClick={(e) => { e.preventDefault(); navigate(linkTo) }}
    >
      <span className="aov-kpi-label">{label}</span>
      <span className="aov-kpi-value-row">
        <span className="aov-kpi-value">{value}</span>
        {valueSuffix && <span className="aov-kpi-value-suffix">{valueSuffix}</span>}
      </span>
      {sublines && (
        <ul className="aov-kpi-sublines">
          {sublines.map((s, i) => (
            <li key={i} className={`aov-kpi-subline aov-kpi-subline--${s.tone || 'neutral'}`}>
              <span className="aov-kpi-subline-label">{s.label}</span>
              <span className="aov-kpi-subline-value">{s.value}</span>
            </li>
          ))}
        </ul>
      )}
      <span className="aov-kpi-link">
        {linkLabel || 'Abrir'} <ArrowIcon />
      </span>
    </a>
  )
}

// ── ServicePill ─────────────────────────────────────────────────────────────
//
// One service in the inline status strip. Dot + name + status word. Hover
// (and focus) reveals latency/detail via native `title` attribute. We
// intentionally avoid a custom popover for v1 — `title` is WCAG-compliant
// for supplemental info and zero-cost.
function ServicePill({ id, status, labels }) {
  const info = labels[id] ?? { name: id, desc: '', env: '' }
  const st = status?.status ?? 'disabled'
  const dot = st === 'ok' ? <CheckIcon /> : st === 'down' ? <XIcon /> : <DashIcon />

  const tooltipParts = [info.desc, info.env && `Var: ${info.env}`]
  if (status?.latency_ms != null && st === 'ok') tooltipParts.push(`Latência: ${fmtLatency(status.latency_ms)}`)
  if (status?.detail && st !== 'ok')             tooltipParts.push(status.detail)
  const tooltip = tooltipParts.filter(Boolean).join(' · ')

  return (
    <span
      className={`aov-pill aov-pill--${st}`}
      title={tooltip}
      tabIndex={0}
      role="status"
      aria-label={`${info.name}: ${st === 'ok' ? 'no ar' : st === 'down' ? 'fora' : 'não usa'}`}
    >
      <span className="aov-pill-dot">{dot}</span>
      <span className="aov-pill-name">{info.name}</span>
      {status?.latency_ms != null && st === 'ok' && (
        <span className="aov-pill-latency">{fmtLatency(status.latency_ms)}</span>
      )}
      {status?.detail && st === 'down' && (
        <span className="aov-pill-detail">{status.detail}</span>
      )}
    </span>
  )
}

// ── ServiceStrip ────────────────────────────────────────────────────────────
//
// Two horizontal strips of pills (infra · obs). Replaces the v1 5-card +
// 3-card grids. Cleaner read and uses zero vertical real estate compared to
// the card grid — appropriate for the "ambient" half of the dual-context
// brief.
function ServiceStrip({ infra, obs, orientation }) {
  return (
    <section className={`aov-strip aov-strip--${orientation || 'horizontal'}`}>
      <div className="aov-strip-group">
        <h2 className="aov-strip-label">Infraestrutura</h2>
        <div className="aov-strip-pills">
          {['postgres','nats','redis','minio','clap_verifier'].map((id) => (
            <ServicePill key={id} id={id} status={infra?.[id]} labels={INFRA_LABELS} />
          ))}
        </div>
      </div>
      <div className="aov-strip-group">
        <h2 className="aov-strip-label">Observabilidade</h2>
        <div className="aov-strip-pills">
          {['prometheus','grafana','jaeger'].map((id) => (
            <ServicePill key={id} id={id} status={obs?.[id]} labels={OBS_LABELS} />
          ))}
        </div>
      </div>
    </section>
  )
}

// ── AttentionCard ───────────────────────────────────────────────────────────
//
// In incident mode, each attention row becomes a full evidence card with a
// top border in semantic color (NOT a side stripe — that's an impeccable
// absolute-ban). Whole card is clickable; small "Silenciar 5min" link in the
// footer hides the item client-side without persisting.
function AttentionCard({ item, onAction, onDismiss }) {
  const sevTone = item.severity === 'critical' ? 'crit' : 'warn'
  const reasonTag = item.reason ? REASON_LABEL[item.reason] || item.reason : null

  return (
    <article className={`aov-card aov-card--${sevTone}`}>
      <button
        type="button"
        className="aov-card-main"
        onClick={() => onAction(item)}
      >
        <span className="aov-card-icon" aria-hidden="true"><AlertIcon /></span>
        <span className="aov-card-body">
          <span className="aov-card-title">
            <span className="aov-card-title-name">{item.title}</span>
            {reasonTag && <span className={`aov-card-tag aov-card-tag--${sevTone}`}>{reasonTag}</span>}
          </span>
          <span className="aov-card-detail">{item.detail}</span>
        </span>
        {item.action_label && (
          <span className="aov-card-action">
            {item.action_label} <ArrowIcon />
          </span>
        )}
      </button>
      <button
        type="button"
        className="aov-card-dismiss"
        onClick={() => onDismiss(item)}
        title="Silenciar este alerta por 5 minutos (apenas local, não persiste)"
      >
        <HushIcon /> Silenciar 5min
      </button>
    </article>
  )
}

// ── EmptyAttention ──────────────────────────────────────────────────────────
//
// Educational empty state per DESIGN.md §4.7. Explains the promise of the
// attention zone so first-timers understand what would appear if something
// broke. Never just "Nothing here."
function EmptyAttention() {
  return (
    <section className="aov-empty">
      <div className="aov-empty-mark" aria-hidden="true">
        <CheckIcon />
      </div>
      <div className="aov-empty-body">
        <h2 className="aov-empty-title">Nada exigindo atenção.</h2>
        <p className="aov-empty-text">
          Workers, streams e pipeline respondendo normalmente. Quando algo quebrar — worker
          ausente, stream caída, infra fora — aparece aqui como um cartão clicável que leva
          direto à ferramenta de correção.
        </p>
      </div>
    </section>
  )
}

// ── Skeleton ────────────────────────────────────────────────────────────────
//
// Match the nominal-mode geometry so first paint doesn't reflow when data
// arrives. Hero ~32vh + 3 KPIs + strip + footer placeholder.
function Skeleton() {
  return (
    <div className="aov" data-mode="nominal" aria-busy="true">
      <div className="aov-hero aov-hero--ok aov-hero--skel" />
      <div className="aov-nominal-grid">
        <div className="aov-kpi aov-kpi--skel" />
        <div className="aov-kpi aov-kpi--skel" />
        <div className="aov-kpi aov-kpi--skel" />
      </div>
      <div className="aov-strip aov-strip--skel" />
    </div>
  )
}

// ── Hook: ring buffer for the pulse hero ────────────────────────────────────
//
// Keeps two parallel sample series, one for the live-stream ratio (foreground)
// and one for detection intensity (background). On every successful refetch we
// push one new sample into each. On first paint we pre-fill with the current
// value so the trace renders immediately instead of starting from a single
// dot.
function usePulseBuffer(data, dataUpdatedAt, maxLen = 60) {
  const [samples, setSamples] = useState({ fg: [], bg: [] })
  const lastTickRef = useRef(0)

  useEffect(() => {
    if (!data) return
    if (dataUpdatedAt === lastTickRef.current) return
    lastTickRef.current = dataUpdatedAt

    const expected = data.streams?.expected_active ?? 0
    const live = data.streams?.live_now ?? 0
    // Foreground: 0..100 amplitude representing % of streams currently live.
    // When expected=0 we float at the baseline (50) so the trace isn't dead.
    const fgRaw = expected > 0 ? (live / expected) * 100 : 50
    // Smooth slightly toward the previous sample so the line doesn't jitter
    // wildly on small changes; gives the trace a calm, wave-like motion.
    const detections = data.data_pipeline?.detections_1h ?? 0
    // Background: detections-per-hour normalized to a 0..60 band sitting under
    // the baseline. The log-ish compression keeps a 5-vs-500 detection range
    // legible without a separate axis.
    const bgRaw = 50 - Math.min(40, Math.log10(detections + 1) * 14)

    setSamples((prev) => {
      const nextFg = appendSample(prev.fg, fgRaw, maxLen)
      const nextBg = appendSample(prev.bg, bgRaw, maxLen)
      // First-paint pre-fill: if we just got our first sample, seed the
      // buffer with that value so the trace already has shape on screen.
      if (prev.fg.length === 0) {
        return {
          fg: Array.from({ length: Math.min(maxLen, 12) }, () => fgRaw),
          bg: Array.from({ length: Math.min(maxLen, 12) }, () => bgRaw),
        }
      }
      return { fg: nextFg, bg: nextBg }
    })
  }, [data, dataUpdatedAt, maxLen])

  return samples
}

function appendSample(arr, value, maxLen) {
  const next = arr.length >= maxLen ? arr.slice(1) : arr.slice()
  next.push(value)
  return next
}

// ── Page ────────────────────────────────────────────────────────────────────
export default function AdminOverviewPage() {
  const navigate = useNavigate()
  const { data, isLoading, isFetching, error, refetch, dataUpdatedAt } = useQuery({
    queryKey: ['admin-system-health'],
    queryFn: async () => (await api.get('/admin/system-health')).data,
    refetchInterval: 10_000,
    staleTime: 5_000,
  })

  // Client-side dismissal of attention items. Pure local state — refreshing
  // the page clears it. Operators wanted to be able to quiet a known item
  // for a few minutes while they work on something else.
  const [dismissed, setDismissed] = useState({})

  // Keyboard shortcut: R triggers refetch from anywhere on the page.
  // Ignored when the user is typing in an input or has a modifier held so
  // we don't collide with browser keybindings.
  useEffect(() => {
    function onKey(e) {
      if (e.key !== 'r' && e.key !== 'R') return
      if (e.metaKey || e.ctrlKey || e.altKey) return
      const tag = (e.target?.tagName || '').toLowerCase()
      if (tag === 'input' || tag === 'textarea' || e.target?.isContentEditable) return
      e.preventDefault()
      refetch()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [refetch])

  const samples = usePulseBuffer(data, dataUpdatedAt)

  const overall = data?.overall ?? 'healthy'
  const isIncident = overall !== 'healthy'

  const lastUpdated = dataUpdatedAt ? fmtRelative(new Date(dataUpdatedAt).toISOString()) : '—'

  const visibleAttention = useMemo(() => {
    const now = Date.now()
    return (data?.attention || []).filter((item) => {
      const key = `${item.station_id || item.kind}-${item.reason || ''}`
      const until = dismissed[key]
      return !until || until < now
    })
  }, [data?.attention, dismissed])

  const handleAction = useCallback((item) => {
    if (item.action_url) navigate(item.action_url)
  }, [navigate])

  const handleDismiss = useCallback((item) => {
    const key = `${item.station_id || item.kind}-${item.reason || ''}`
    setDismissed((prev) => ({ ...prev, [key]: Date.now() + 5 * 60_000 }))
  }, [])

  if (error) {
    return (
      <div className="aov" data-mode="nominal">
        <div className="aov-error">
          <h1 className="aov-error-title">Não foi possível ler o painel de health.</h1>
          <p className="aov-error-text">
            O endpoint <code>/admin/system-health</code> não respondeu. Pode ser problema
            de rede, do API container, ou que sua sessão expirou.
          </p>
          <button type="button" className="aov-error-btn" onClick={() => refetch()}>
            Tentar novamente
          </button>
        </div>
      </div>
    )
  }

  if (isLoading) return <Skeleton />

  const headline = buildHeadline(data)
  const workers = data?.workers ?? {}
  const streams = data?.streams ?? {}
  const pipeline = data?.data_pipeline ?? {}

  return (
    <div className="aov" data-mode={isIncident ? 'incident' : 'nominal'}>
      <PulseHero
        data={data}
        overall={overall}
        samples={samples}
        isFetching={isFetching}
        lastUpdatedRel={lastUpdated}
        onRefresh={refetch}
        isIncident={isIncident}
        headline={headline}
      />

      {isIncident ? (
        <div className="aov-incident-grid">
          {/* Left: attention column (2/3 width on desktop) */}
          <section className="aov-attention">
            <header className="aov-attention-head">
              <h2 className="aov-attention-title">
                Atenção agora
                {visibleAttention.length > 0 && (
                  <span className="aov-attention-count">{visibleAttention.length}</span>
                )}
              </h2>
              <p className="aov-attention-sub">
                Itens classificados por severidade. Clique no cartão pra ir direto à ferramenta de correção.
              </p>
            </header>
            {visibleAttention.length > 0 ? (
              <div className="aov-attention-list">
                {visibleAttention
                  .slice()
                  .sort((a, b) => (a.severity === 'critical' ? -1 : 0) - (b.severity === 'critical' ? -1 : 0))
                  .map((item, i) => (
                    <AttentionCard
                      key={`${item.station_id || 'sys'}-${i}`}
                      item={item}
                      onAction={handleAction}
                      onDismiss={handleDismiss}
                    />
                  ))}
              </div>
            ) : (
              // Possible only when everything currently in attention was just
              // dismissed locally. Treat it like a calm interstitial, not a
              // celebration — the system is still degraded.
              <div className="aov-attention-quieted">
                <HushIcon /> Todos os alertas silenciados localmente. <button type="button" onClick={() => setDismissed({})}>Reexibir</button>
              </div>
            )}
          </section>

          {/* Right: condensed status column */}
          <aside className="aov-status">
            <KpiBlock
              compact
              label="Workers"
              value={`${workers.running ?? 0}`}
              valueSuffix={`/${workers.expected_active ?? 0}`}
              tone={(workers.missing ?? 0) > 0 ? 'crit' : (workers.stalled ?? 0) > 0 ? 'warn' : 'ok'}
              sublines={[
                { label: 'Travados',  value: workers.stalled ?? 0, tone: (workers.stalled ?? 0) > 0 ? 'warn' : 'neutral' },
                { label: 'Ausentes',  value: workers.missing ?? 0, tone: (workers.missing ?? 0) > 0 ? 'crit' : 'neutral' },
              ]}
              linkTo="/operations"
              linkLabel="Workers"
              navigate={navigate}
            />
            <KpiBlock
              compact
              label="Streams ao ar"
              value={`${streams.live_now ?? 0}`}
              valueSuffix={`/${streams.expected_active ?? 0}`}
              tone={(streams.down_now ?? 0) > 0 ? 'warn' : 'ok'}
              sublines={[
                { label: 'Fora agora', value: streams.down_now ?? 0, tone: (streams.down_now ?? 0) > 0 ? 'warn' : 'neutral' },
                { label: 'Quedas 24h', value: streams.incidents_24h ?? 0, tone: 'neutral' },
              ]}
              linkTo="/monitoring"
              linkLabel="Streams"
              navigate={navigate}
            />
            <KpiBlock
              compact
              label="Pipeline 1h"
              value={`${pipeline.detections_1h ?? 0}`}
              tone="ok"
              sublines={[
                { label: 'Última detecção', value: fmtRelative(pipeline.last_detection_at), tone: 'neutral' },
                { label: 'Webhooks falhos', value: pipeline.webhooks_failed_24h ?? 0, tone: (pipeline.webhooks_failed_24h ?? 0) > 0 ? 'warn' : 'neutral' },
              ]}
              linkTo="/detections"
              linkLabel="Detecções"
              navigate={navigate}
            />
            <ServiceStrip infra={data?.infrastructure} obs={data?.observability} orientation="vertical" />
          </aside>
        </div>
      ) : (
        <>
          <div className="aov-nominal-grid">
            <KpiBlock
              label="Workers"
              value={`${workers.running ?? 0}`}
              valueSuffix={`/${workers.expected_active ?? 0}`}
              tone={(workers.missing ?? 0) > 0 ? 'crit' : (workers.stalled ?? 0) > 0 ? 'warn' : 'ok'}
              sublines={[
                { label: 'Em execução', value: workers.running ?? 0, tone: 'ok' },
                { label: 'Travados',    value: workers.stalled ?? 0, tone: 'neutral' },
                { label: 'Ausentes',    value: workers.missing ?? 0, tone: 'neutral' },
              ]}
              linkTo="/operations"
              linkLabel="Abrir workers"
              navigate={navigate}
            />
            <KpiBlock
              label="Streams ao ar"
              value={`${streams.live_now ?? 0}`}
              valueSuffix={`/${streams.expected_active ?? 0}`}
              tone={(streams.down_now ?? 0) > 0 ? 'warn' : 'ok'}
              sublines={[
                { label: 'Fora agora',  value: streams.down_now ?? 0, tone: 'neutral' },
                { label: `Queda${plural(streams.incidents_24h ?? 0, '', 's')} 24h`, value: streams.incidents_24h ?? 0, tone: 'neutral' },
              ]}
              linkTo="/monitoring"
              linkLabel="Abrir streams"
              navigate={navigate}
            />
            <KpiBlock
              label="Pipeline 1h"
              value={`${pipeline.detections_1h ?? 0}`}
              tone="ok"
              sublines={[
                { label: 'Última detecção',     value: fmtRelative(pipeline.last_detection_at), tone: 'neutral' },
                { label: 'Webhooks pendentes',  value: pipeline.webhooks_pending ?? 0, tone: 'neutral' },
                { label: 'Webhooks falhos 24h', value: pipeline.webhooks_failed_24h ?? 0, tone: (pipeline.webhooks_failed_24h ?? 0) > 0 ? 'warn' : 'neutral' },
              ]}
              linkTo="/detections"
              linkLabel="Abrir detecções"
              navigate={navigate}
            />
          </div>

          <ServiceStrip infra={data?.infrastructure} obs={data?.observability} />

          <EmptyAttention />
        </>
      )}
    </div>
  )
}
