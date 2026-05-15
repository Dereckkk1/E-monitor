import { useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import api from '../api/client'
import './AdminOverviewPage.css'

/*
 * AdminOverviewPage — /admin/overview
 *
 * Single screen that aggregates every component the system depends on into
 * one panoramic health view. Powered by GET /admin/system-health which
 * pings each dependency directly (postgres, NATS, Redis, MinIO, CLAP,
 * Prometheus, Grafana, Jaeger) and rolls up workers/streams/data-pipeline
 * counters plus a classified "attention" list of currently-broken things.
 *
 * Layout follows pro-system-ui rules:
 *   - Top rollup banner = the one-glance answer.
 *   - Infra + observability grids = the "is everything reachable?" check.
 *   - KPI band (workers/streams/pipeline) = the runtime numbers.
 *   - Attention list = the actionable "what's broken right now?" zone with
 *     deep links to the existing /operations and /monitoring pages.
 *
 * Refetch every 10s — same cadence as /operations, fast enough to feel
 * live without hammering the API.
 */

// ── Status meta (color + label per status) ──────────────────────────────────
const SERVICE_META = {
  ok:       { label: 'No ar',    cls: 'aoh-pill--ok' },
  down:     { label: 'Fora',     cls: 'aoh-pill--down' },
  disabled: { label: 'Não usa',  cls: 'aoh-pill--muted' },
}

const SEVERITY_META = {
  warning:  { cls: 'aoh-att--warning' },
  critical: { cls: 'aoh-att--critical' },
}

const INFRA_LABELS = {
  postgres:      { name: 'PostgreSQL',  desc: 'Banco principal' },
  nats:          { name: 'NATS',        desc: 'Fila de eventos' },
  redis:         { name: 'Redis',       desc: 'Cache em memória' },
  minio:         { name: 'MinIO / S3',  desc: 'Storage de evidência' },
  clap_verifier: { name: 'CLAP',        desc: 'Verificador neural' },
}

const OBS_LABELS = {
  prometheus: { name: 'Prometheus', desc: 'Coleta de métricas' },
  grafana:    { name: 'Grafana',    desc: 'Dashboards' },
  jaeger:     { name: 'Jaeger',     desc: 'Tracing distribuído' },
}

const OVERALL_META = {
  healthy:  { label: 'Sistema saudável',     cls: 'aoh-rollup--ok' },
  degraded: { label: 'Sistema degradado',    cls: 'aoh-rollup--warn' },
  critical: { label: 'Sistema crítico',      cls: 'aoh-rollup--crit' },
}

// ── Helpers ─────────────────────────────────────────────────────────────────
function fmtRelative(iso) {
  if (!iso) return '—'
  const ms = Date.now() - new Date(iso).getTime()
  if (Number.isNaN(ms) || ms < 0) return 'agora'
  if (ms < 60_000)        return `há ${Math.floor(ms / 1000)}s`
  if (ms < 3_600_000)     return `há ${Math.floor(ms / 60_000)}min`
  if (ms < 86_400_000)    return `há ${Math.floor(ms / 3_600_000)}h`
  return `há ${Math.floor(ms / 86_400_000)}d`
}

function fmtLatency(ms) {
  if (ms == null) return null
  if (ms < 1)   return '<1ms'
  if (ms < 100) return `${ms}ms`
  return `${(ms / 1000).toFixed(2)}s`
}

// ── Icons ───────────────────────────────────────────────────────────────────
function CheckIcon()  { return <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M3 8.5l3 3 7-7"/></svg> }
function XIcon()      { return <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><path d="M4 4l8 8M12 4l-8 8"/></svg> }
function DashIcon()   { return <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><path d="M3.5 8h9"/></svg> }
function WarnIcon()   { return <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round"><path d="M8 1.5l7 12.5H1L8 1.5z"/><path d="M8 6.5v3.5"/><circle cx="8" cy="12" r="0.6" fill="currentColor" stroke="none"/></svg> }
function ArrowIcon()  { return <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M5 3l5 5-5 5"/></svg> }
function PulseIcon()  { return <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round"><path d="M1 8h3l2-5 3 10 2-5h4"/></svg> }
function ReloadIcon() { return <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round"><path d="M14 8a6 6 0 1 1-1.76-4.24"/><path d="M14 2v4h-4"/></svg> }

// ── Subcomponents ───────────────────────────────────────────────────────────

function ServiceCard({ id, status, labels }) {
  const meta = SERVICE_META[status?.status] ?? SERVICE_META.disabled
  const info = labels[id] ?? { name: id, desc: '' }
  const dot =
    status?.status === 'ok'   ? <CheckIcon /> :
    status?.status === 'down' ? <XIcon /> :
    <DashIcon />
  return (
    <div className={`aoh-svc aoh-svc--${status?.status ?? 'disabled'}`}>
      <div className="aoh-svc-head">
        <span className={`aoh-svc-dot ${meta.cls}`}>{dot}</span>
        <div className="aoh-svc-name">{info.name}</div>
      </div>
      <div className="aoh-svc-desc">{info.desc}</div>
      <div className="aoh-svc-foot">
        <span className={`aoh-pill ${meta.cls}`}>{meta.label}</span>
        {status?.latency_ms != null && status.status === 'ok' && (
          <span className="aoh-svc-latency">{fmtLatency(status.latency_ms)}</span>
        )}
        {status?.detail && status.status !== 'ok' && (
          <span className="aoh-svc-detail" title={status.detail}>{status.detail}</span>
        )}
      </div>
    </div>
  )
}

function KpiBlock({ label, value, accent, sublines }) {
  return (
    <div className={`aoh-kpi ${accent ? `aoh-kpi--${accent}` : ''}`}>
      <div className="aoh-kpi-label">{label}</div>
      <div className="aoh-kpi-value">{value}</div>
      {sublines && (
        <div className="aoh-kpi-sub">
          {sublines.map((s, i) => (
            <div key={i} className="aoh-kpi-sub-row">
              <span className={`aoh-kpi-sub-dot aoh-kpi-sub-dot--${s.tone || 'neutral'}`} />
              <span className="aoh-kpi-sub-label">{s.label}</span>
              <span className="aoh-kpi-sub-value">{s.value}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function AttentionRow({ item, onAction }) {
  const meta = SEVERITY_META[item.severity] ?? SEVERITY_META.warning
  return (
    <button type="button" className={`aoh-att-row ${meta.cls}`} onClick={() => onAction(item)}>
      <span className="aoh-att-icon"><WarnIcon /></span>
      <div className="aoh-att-body">
        <div className="aoh-att-title">
          <span className="aoh-att-title-name">{item.title}</span>
          {item.reason && <span className="aoh-att-tag">{labelForReason(item.reason)}</span>}
        </div>
        <div className="aoh-att-detail">{item.detail}</div>
      </div>
      {item.action_label && (
        <span className="aoh-att-action">
          {item.action_label} <ArrowIcon />
        </span>
      )}
    </button>
  )
}

function labelForReason(r) {
  switch (r) {
    case 'not_registered': return 'Não registrado'
    case 'stalled':        return 'Travado'
    case 'stream_down':    return 'Stream fora'
    default:               return r
  }
}

// Skeleton placeholders, density-matched to the loaded screen so the
// transition doesn't feel like a reflow. design.md §4.6.
function Skeleton({ className }) {
  return <div className={`aoh-skel ${className || ''}`} />
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

  const overall = data?.overall ?? 'healthy'
  const overallMeta = OVERALL_META[overall] ?? OVERALL_META.healthy

  const lastUpdated = dataUpdatedAt ? fmtRelative(new Date(dataUpdatedAt).toISOString()) : '—'

  const infraEntries = useMemo(() => {
    const order = ['postgres', 'nats', 'redis', 'minio', 'clap_verifier']
    return order.map((k) => ({ id: k, status: data?.infrastructure?.[k] }))
  }, [data])

  const obsEntries = useMemo(() => {
    const order = ['prometheus', 'grafana', 'jaeger']
    return order.map((k) => ({ id: k, status: data?.observability?.[k] }))
  }, [data])

  function handleAction(item) {
    if (item.action_url) navigate(item.action_url)
  }

  if (error) {
    return (
      <div className="aoh">
        <div className="aoh-error">
          <h2>Não foi possível carregar o painel</h2>
          <p>A API retornou erro ao consultar <code>/admin/system-health</code>.</p>
          <button className="aoh-btn aoh-btn--primary" onClick={() => refetch()}>Tentar novamente</button>
        </div>
      </div>
    )
  }

  return (
    <div className="aoh">
      {/* ── Rollup banner ──────────────────────────────────────────────── */}
      <header className={`aoh-rollup ${overallMeta.cls}`}>
        <div className="aoh-rollup-main">
          <div className="aoh-rollup-pulse" aria-hidden="true">
            <span className="aoh-rollup-pulse-dot" />
          </div>
          <div className="aoh-rollup-text">
            <h1 className="aoh-rollup-title">{overallMeta.label}</h1>
            <p className="aoh-rollup-sub">
              Visão geral do health de toda a plataforma. Tudo que pode quebrar fica aqui.
            </p>
          </div>
        </div>
        <div className="aoh-rollup-meta">
          <div className="aoh-rollup-updated">
            <span className="aoh-rollup-updated-label">Atualizado</span>
            <span className="aoh-rollup-updated-value">{lastUpdated}</span>
          </div>
          <button
            type="button"
            className={`aoh-refresh ${isFetching ? 'aoh-refresh--spin' : ''}`}
            onClick={() => refetch()}
            aria-label="Atualizar agora"
            title="Atualizar agora"
          >
            <ReloadIcon />
          </button>
        </div>
      </header>

      {/* ── Infrastructure ─────────────────────────────────────────────── */}
      <section className="aoh-section">
        <div className="aoh-section-head">
          <h2>Infraestrutura</h2>
          <p>Serviços críticos. Se algum cair, a captura ou o armazenamento para.</p>
        </div>
        <div className="aoh-grid aoh-grid--infra">
          {isLoading
            ? Array.from({ length: 5 }).map((_, i) => <Skeleton key={i} className="aoh-skel--svc" />)
            : infraEntries.map(({ id, status }) => (
                <ServiceCard key={id} id={id} status={status} labels={INFRA_LABELS} />
              ))}
        </div>
      </section>

      {/* ── Observability ──────────────────────────────────────────────── */}
      <section className="aoh-section">
        <div className="aoh-section-head">
          <h2>Observabilidade</h2>
          <p>Telemetria e dashboards. Pode estar fora sem derrubar a operação — mas o operador fica cego.</p>
        </div>
        <div className="aoh-grid aoh-grid--obs">
          {isLoading
            ? Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="aoh-skel--svc" />)
            : obsEntries.map(({ id, status }) => (
                <ServiceCard key={id} id={id} status={status} labels={OBS_LABELS} />
              ))}
        </div>
      </section>

      {/* ── Runtime KPIs ───────────────────────────────────────────────── */}
      <section className="aoh-section">
        <div className="aoh-section-head">
          <h2>Operação ao vivo</h2>
          <p>Workers, streams e pipeline de detecção no último ciclo.</p>
        </div>
        <div className="aoh-grid aoh-grid--kpi">
          {isLoading ? (
            <>
              <Skeleton className="aoh-skel--kpi" />
              <Skeleton className="aoh-skel--kpi" />
              <Skeleton className="aoh-skel--kpi" />
            </>
          ) : (
            <>
              <KpiBlock
                label="Workers"
                value={`${data?.workers?.running ?? 0}/${data?.workers?.expected_active ?? 0}`}
                accent={
                  (data?.workers?.missing ?? 0) > 0 ? 'crit' :
                  (data?.workers?.stalled ?? 0) > 0 ? 'warn' : 'ok'
                }
                sublines={[
                  { label: 'Em execução', value: data?.workers?.running ?? 0, tone: 'ok' },
                  { label: 'Travados',    value: data?.workers?.stalled ?? 0, tone: (data?.workers?.stalled ?? 0) > 0 ? 'warn' : 'neutral' },
                  { label: 'Ausentes',    value: data?.workers?.missing ?? 0, tone: (data?.workers?.missing ?? 0) > 0 ? 'crit' : 'neutral' },
                ]}
              />
              <KpiBlock
                label="Streams ao ar"
                value={`${data?.streams?.live_now ?? 0}/${data?.streams?.expected_active ?? 0}`}
                accent={
                  (data?.streams?.down_now ?? 0) > 0 ? 'warn' : 'ok'
                }
                sublines={[
                  { label: 'Fora agora',    value: data?.streams?.down_now ?? 0, tone: (data?.streams?.down_now ?? 0) > 0 ? 'warn' : 'neutral' },
                  { label: 'Quedas 24h',    value: data?.streams?.incidents_24h ?? 0, tone: 'neutral' },
                ]}
              />
              <KpiBlock
                label="Pipeline de detecção"
                value={`${data?.data_pipeline?.detections_1h ?? 0}`}
                accent="ok"
                sublines={[
                  { label: 'Última detecção',     value: fmtRelative(data?.data_pipeline?.last_detection_at), tone: 'neutral' },
                  { label: 'Webhooks pendentes',  value: data?.data_pipeline?.webhooks_pending ?? 0, tone: (data?.data_pipeline?.webhooks_pending ?? 0) > 10 ? 'warn' : 'neutral' },
                  { label: 'Webhooks falhos 24h', value: data?.data_pipeline?.webhooks_failed_24h ?? 0, tone: (data?.data_pipeline?.webhooks_failed_24h ?? 0) > 0 ? 'warn' : 'neutral' },
                ]}
              />
            </>
          )}
        </div>
      </section>

      {/* ── Attention list ─────────────────────────────────────────────── */}
      <section className="aoh-section aoh-section--attention">
        <div className="aoh-section-head">
          <h2>
            Atenção agora
            {data?.attention?.length > 0 && (
              <span className="aoh-section-count">{data.attention.length}</span>
            )}
          </h2>
          <p>Lista priorizada do que está quebrado agora. Clique em uma linha pra ir direto à ferramenta de correção.</p>
        </div>
        {isLoading ? (
          <div className="aoh-att-list">
            <Skeleton className="aoh-skel--att" />
            <Skeleton className="aoh-skel--att" />
          </div>
        ) : data?.attention?.length > 0 ? (
          <div className="aoh-att-list">
            {data.attention.map((item, i) => (
              <AttentionRow key={`${item.station_id || 'sys'}-${i}`} item={item} onAction={handleAction} />
            ))}
          </div>
        ) : (
          <div className="aoh-att-empty">
            <span className="aoh-att-empty-icon"><PulseIcon /></span>
            <div>
              <strong>Nada exigindo atenção.</strong>
              <span>Workers, streams e pipeline estão respondendo normalmente.</span>
            </div>
          </div>
        )}
      </section>
    </div>
  )
}
