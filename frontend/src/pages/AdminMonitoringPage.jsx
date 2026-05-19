import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { createPortal } from 'react-dom'
import api from '../api/client'
import './AdminMonitoringPage.css'

/*
 * AdminMonitoringPage — /admin/monitoring
 *
 * Painel admin de telemetria HTTP — espelho do /admin/monitoring do
 * E-radios/Signalads, adaptado para a stack Go + design tokens do Radiocheck.
 *
 * Cinco tabs:
 *   - Visão Geral: KPIs de período + server strip + timeline de requests
 *   - Identidades: tabela de atores (IP × usuário) com risco automático,
 *     painel detalhado por ator, sub-tab de IPs bloqueados
 *   - Performance: latência por rota (p50/p95/p99/health)
 *   - Web Vitals: LCP/INP/CLS/FCP/TTFB agregados por página
 *   - Erros: top 5xx + requests lentos (>2s)
 *
 * Backend: workers/internal/api/handlers/admin_monitoring.go +
 * workers/internal/reqmetrics/* (middleware async).
 * Docs: docs/features/admin-monitoring.md.
 */

// ── Constantes ─────────────────────────────────────────────────────────────

const RANGES = [
  { value: '1h',  label: '1h' },
  { value: '24h', label: '24h' },
  { value: '7d',  label: '7d' },
  { value: '30d', label: '30d' },
]

const RISK_LABELS = { low: 'Baixo', medium: 'Médio', high: 'Alto', critical: 'Crítico' }
const PER_PAGE = 15

const VITAL_DESC = {
  LCP: 'Largest Contentful Paint — Tempo até o maior elemento visível carregar. Bom < 2.5s | Ruim > 4s',
  FID: 'First Input Delay — Atraso entre o primeiro clique e a resposta. Bom < 100ms | Ruim > 300ms',
  INP: 'Interaction to Next Paint — Latência de interações. Substitui o FID. Bom < 200ms | Ruim > 500ms',
  CLS: 'Cumulative Layout Shift — Quanto o layout "pula". Score adim. Bom < 0.1 | Ruim > 0.25',
  FCP: 'First Contentful Paint — Tempo até aparecer o primeiro conteúdo. Bom < 1.8s | Ruim > 3s',
  TTFB: 'Time to First Byte — Tempo até o servidor responder o primeiro byte. Bom < 800ms | Ruim > 1.8s',
}

// ── Formatters ─────────────────────────────────────────────────────────────

function fmtMs(ms) {
  if (ms == null) return '—'
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(2)}s`
}
function fmtNum(n) { return (n ?? 0).toLocaleString('pt-BR') }
function fmtDate(d) {
  if (!d) return '—'
  return new Date(d).toLocaleString('pt-BR', {
    day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit',
  })
}
function fmtDateSec(d) {
  if (!d) return '—'
  return new Date(d).toLocaleString('pt-BR', {
    day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

// ── Ícones (inline SVG — sem dependência externa) ───────────────────────────

const Icon = {
  Monitor: () => <svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round"><rect x="2" y="3" width="16" height="11" rx="1.5"/><path d="M6 17h8M10 14v3"/></svg>,
  Refresh: () => <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M14 8a6 6 0 1 1-1.76-4.24"/><path d="M14 2v4h-4"/></svg>,
  Speed:   () => <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M8 2a6 6 0 0 0-6 6h12a6 6 0 0 0-6-6Z"/><path d="m8 8 3-2"/></svg>,
  Timer:   () => <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="8" cy="9" r="5.5"/><path d="M8 6v3.5L10 11M6 2h4"/></svg>,
  Error:   () => <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M8 1.5 1 14h14L8 1.5Z"/><path d="M8 6.5v3.5"/><circle cx="8" cy="12" r="0.6" fill="currentColor" stroke="none"/></svg>,
  Timeline:() => <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M2 12h12M2 8h12M2 4h12"/></svg>,
  Shield:  () => <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M8 1.5 2.5 4v4.5c0 3.3 2.4 5.6 5.5 6 3.1-.4 5.5-2.7 5.5-6V4L8 1.5Z"/></svg>,
  Web:     () => <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="8" cy="8" r="6"/><path d="M2 8h12M8 2c2 2.5 2 9 0 12M8 2c-2 2.5-2 9 0 12"/></svg>,
  Search:  () => <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="7" cy="7" r="5"/><path d="M11 11l3 3"/></svg>,
  Close:   () => <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><path d="M4 4l8 8M12 4l-8 8"/></svg>,
  Block:   () => <svg width="11" height="11" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round"><circle cx="8" cy="8" r="6"/><path d="M3.5 3.5l9 9"/></svg>,
  Copy:    () => <svg width="11" height="11" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><rect x="5" y="5" width="9" height="9" rx="1.5"/><path d="M3 11V3a1 1 0 0 1 1-1h7"/></svg>,
  Check:   () => <svg width="11" height="11" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M3 8.5 6 11.5 13 4.5"/></svg>,
  Memory:  () => <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="3" width="10" height="10" rx="1"/><path d="M5 6h6M5 8h6M5 10h6"/></svg>,
  Server:  () => <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"><rect x="2" y="3" width="12" height="4" rx="1"/><rect x="2" y="9" width="12" height="4" rx="1"/><circle cx="4.5" cy="5" r="0.4" fill="currentColor" stroke="none"/><circle cx="4.5" cy="11" r="0.4" fill="currentColor" stroke="none"/></svg>,
  Person:  () => <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="8" cy="5.5" r="2.5"/><path d="M2.5 13.5c0-3 2.5-4.5 5.5-4.5s5.5 1.5 5.5 4.5"/></svg>,
  Anon:    () => <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="8" cy="8" r="6"/><path d="M5.5 9.5c0-1 1-2 2.5-2s2.5 1 2.5 2"/><path d="M6.5 6.5h.01M9.5 6.5h.01"/></svg>,
  Info:    () => <svg width="11" height="11" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="8" cy="8" r="6"/><path d="M8 7v4"/><circle cx="8" cy="5" r="0.5" fill="currentColor" stroke="none"/></svg>,
  Open:    () => <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round"><path d="M5 3l5 5-5 5"/></svg>,
  ChevDown:() => <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><path d="M4 6l4 4 4-4"/></svg>,
  ChevRight:() => <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><path d="M6 4l4 4-4 4"/></svg>,
  Unlock:  () => <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><rect x="3.5" y="7.5" width="9" height="6" rx="1"/><path d="M5.5 7.5V5.5a2.5 2.5 0 0 1 4.7-1.2"/></svg>,
  Filter:  () => <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M2 3h12l-4.5 6V14L6.5 12.5V9L2 3Z"/></svg>,
}

// ── Tooltip portal (evita clipping em tabelas com overflow) ────────────────
function TipPortal({ visible, target, content }) {
  const [pos, setPos] = useState({ top: 0, left: 0 })
  useEffect(() => {
    if (!visible || !target.current) return
    const r = target.current.getBoundingClientRect()
    setPos({ top: r.top + window.scrollY - 8, left: r.left + r.width / 2 + window.scrollX })
  }, [visible, target])
  if (!visible) return null
  return createPortal(
    <div className="am-tip" style={{ top: pos.top, left: pos.left }}>{content}</div>,
    document.body,
  )
}
function TipIcon({ tip }) {
  const [open, setOpen] = useState(false)
  const ref = useRef(null)
  return (
    <span ref={ref} className="am-tip-icon" onMouseEnter={() => setOpen(true)} onMouseLeave={() => setOpen(false)}>
      <Icon.Info />
      <TipPortal visible={open} target={ref} content={tip} />
    </span>
  )
}
function ThTip({ children, tip }) {
  return <th><span className="am-th-tip">{children}<TipIcon tip={tip} /></span></th>
}

// ── Helpers UI compartilhados ──────────────────────────────────────────────

function RiskBadge({ level }) {
  return <span className={`am-risk am-risk--${level}`}>{RISK_LABELS[level] ?? level}</span>
}

function RequestBar({ count, max }) {
  const pct = max > 0 ? Math.max(2, Math.round((count / max) * 100)) : 0
  return (
    <div className="am-reqbar">
      <span className="am-reqbar-count">{fmtNum(count)}</span>
      <div className="am-reqbar-track">
        <div className="am-reqbar-fill" style={{ width: `${pct}%` }} />
      </div>
    </div>
  )
}

function CopyBtn({ text }) {
  const [copied, setCopied] = useState(false)
  return (
    <button
      type="button"
      className="am-copy"
      onClick={(e) => { e.stopPropagation(); navigator.clipboard.writeText(text); setCopied(true); setTimeout(() => setCopied(false), 1200) }}
      title="Copiar"
    >
      {copied ? <Icon.Check /> : <Icon.Copy />}
    </button>
  )
}

function Pagination({ page, total, onChange }) {
  if (total <= 1) return null
  return (
    <div className="am-paging">
      <button type="button" className="am-paging-btn" disabled={page === 1} onClick={() => onChange(page - 1)}>‹</button>
      <span className="am-paging-info">{page} / {total}</span>
      <button type="button" className="am-paging-btn" disabled={page === total} onClick={() => onChange(page + 1)}>›</button>
    </div>
  )
}

function HealthBadge({ health }) {
  const map = {
    good:     ['ok', 'Saudável'],
    warning:  ['warn', 'Atenção'],
    critical: ['crit', 'Crítico'],
  }
  const [tone, label] = map[health] ?? ['neutral', '—']
  return <span className={`am-health am-health--${tone}`}>{label}</span>
}

// ── Sparkline SVG (timeline + actor activity) ──────────────────────────────
// Stroke + fill suave. coords no espaço 0..N x 0..100 com preserveAspectRatio
// "none" — escala para o container.
function Sparkline({ points, color, fill, height = 220 }) {
  if (!points || points.length === 0) {
    return <div className="am-spark-empty">Sem dados</div>
  }
  const N = Math.max(points.length, 2)
  const max = Math.max(1, ...points)
  const W = 100, H = 100
  const xs = (i) => (i / (N - 1)) * W
  const ys = (v) => H - (v / max) * (H * 0.92)

  let d = ''
  let area = `M0,${H} `
  for (let i = 0; i < points.length; i++) {
    const x = xs(i), y = ys(points[i])
    d += `${i === 0 ? 'M' : 'L'}${x.toFixed(2)},${y.toFixed(2)} `
    area += `L${x.toFixed(2)},${y.toFixed(2)} `
  }
  area += `L${W},${H} Z`

  return (
    <svg className="am-spark" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" style={{ height }}>
      <path d={area} fill={fill} />
      <path d={d} fill="none" stroke={color} strokeWidth="1.4" />
    </svg>
  )
}

// ── Fetch hooks ────────────────────────────────────────────────────────────

function useMonitoring(range, hideLocalhost) {
  const qs = `range=${range}&hideLocalhost=${hideLocalhost}`
  return useQuery({
    queryKey: ['admin-monitoring', range, hideLocalhost],
    queryFn: async () => {
      const [ovr, rts, errs, slow, tl, vit] = await Promise.all([
        api.get(`/admin/monitoring/overview?${qs}`),
        api.get(`/admin/monitoring/routes?${qs}`),
        api.get(`/admin/monitoring/errors?${qs}`),
        api.get(`/admin/monitoring/slow?${qs}`),
        api.get(`/admin/monitoring/timeline?${qs}`),
        api.get(`/admin/monitoring/vitals?${qs}`),
      ])
      return {
        overview: ovr.data,
        routes:   rts.data.routes ?? [],
        errors:   errs.data.errors ?? [],
        slow:     slow.data.requests ?? [],
        timeline: tl.data.timeline ?? [],
        vitals:   vit.data.vitals ?? [],
      }
    },
    refetchInterval: 15_000,
    staleTime: 5_000,
  })
}

function useActors(range, hideLocalhost) {
  const qs = `range=${range}&hideLocalhost=${hideLocalhost}`
  return useQuery({
    queryKey: ['admin-monitoring-actors', range, hideLocalhost],
    queryFn: async () => (await api.get(`/admin/monitoring/top-actors?${qs}`)).data.actors ?? [],
    refetchInterval: 15_000,
  })
}

function useBlockedIPs() {
  return useQuery({
    queryKey: ['admin-monitoring-blocked'],
    queryFn: async () => (await api.get(`/admin/monitoring/blocked-ips`)).data.blockedIPs ?? [],
    refetchInterval: 30_000,
  })
}

// ── Actor Detail Panel (slide-in) ──────────────────────────────────────────

function ActorDetailPanel({ actor, range, onClose, onBlockIp, onBlockUser, onUnblockIp }) {
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!actor) return
    setLoading(true)
    const params = new URLSearchParams({ range })
    if (actor.ip) params.set('ip', actor.ip)
    if (actor.userId) params.set('userId', actor.userId)
    api.get(`/admin/monitoring/actor-detail?${params}`)
      .then((r) => setData(r.data))
      .catch(() => setData(null))
      .finally(() => setLoading(false))
  }, [actor, range])

  if (!actor) return null

  const timelinePoints = (data?.timeline ?? []).map((t) => t.count ?? 0)

  return (
    <>
      <div className="am-panel-overlay" onClick={onClose} />
      <aside className="am-panel" role="dialog" aria-label="Detalhe do ator">
        <header className="am-panel-head">
          <div className="am-panel-actor">
            <div className="am-panel-actor-icon">
              {actor.userId ? <Icon.Person /> : <Icon.Anon />}
            </div>
            <div className="am-panel-actor-meta">
              <div className="am-panel-actor-row">
                {actor.ip ? (
                  <>
                    <code className="am-ip">{actor.ip}</code>
                    <CopyBtn text={actor.ip} />
                  </>
                ) : (
                  <span className="am-panel-multi-ip">
                    {actor.ips ? `${actor.ips.length} IPs` : 'Múltiplos IPs'}
                  </span>
                )}
                <RiskBadge level={actor.riskLevel} />
                {actor.isIPBlocked && (
                  <span className="am-blocked-tag"><Icon.Block /> Bloqueado</span>
                )}
              </div>
              <div className="am-panel-actor-user">
                {actor.userEmail
                  ? (<><Icon.Person /><span>{actor.userEmail}</span><CopyBtn text={actor.userEmail} /></>)
                  : <span className="am-anon">Acesso anônimo</span>}
              </div>
            </div>
          </div>
          <button type="button" className="am-panel-close" onClick={onClose} aria-label="Fechar"><Icon.Close /></button>
        </header>

        <div className="am-panel-stats">
          {[
            { value: fmtNum(actor.totalRequests), label: 'Requests' },
            { value: fmtNum(actor.uniqueRouteCount), label: 'Rotas únicas' },
            { value: fmtNum(actor.errorCount), label: 'Erros 5xx', danger: actor.errorCount > 0 },
            { value: fmtNum(actor.slowCount), label: 'Lentos >2s' },
          ].map((s, i) => (
            <div key={i} className="am-panel-stat">
              <span className={`am-panel-stat-value ${s.danger ? 'am-panel-stat-value--danger' : ''}`}>{s.value}</span>
              <span className="am-panel-stat-label">{s.label}</span>
            </div>
          ))}
        </div>

        <div className="am-panel-period">
          <span>Primeiro: <strong>{fmtDateSec(actor.firstSeen)}</strong></span>
          <span>Último: <strong>{fmtDateSec(actor.lastSeen)}</strong></span>
        </div>

        <div className="am-panel-actions">
          {actor.ip && (actor.isIPBlocked
            ? <button type="button" className="am-action-btn am-action-btn--unblock" onClick={() => onUnblockIp(actor.ip)}><Icon.Unlock /> Desbloquear IP</button>
            : <button type="button" className="am-action-btn am-action-btn--block-ip" onClick={() => onBlockIp(actor.ip)}><Icon.Block /> Bloquear IP</button>
          )}
          {actor.userId && (
            <button type="button" className="am-action-btn am-action-btn--block-user" onClick={() => onBlockUser(actor.userId, actor.userEmail)}>
              <Icon.Block /> Bloquear Usuário
            </button>
          )}
        </div>

        <section className="am-panel-section">
          <h4 className="am-panel-section-title">Atividade por hora</h4>
          {loading ? (
            <div className="am-skel" style={{ height: 130, borderRadius: 'var(--radius-md)' }} />
          ) : (
            <div className="am-spark-wrap">
              <Sparkline points={timelinePoints} color="var(--c-action)" fill="rgba(232,30,117,0.10)" height={130} />
            </div>
          )}
        </section>

        {data?.topRoutes?.length > 0 && (
          <section className="am-panel-section">
            <h4 className="am-panel-section-title">Rotas mais acessadas</h4>
            <div className="am-route-list">
              {data.topRoutes.map((r, i) => (
                <div key={r.route} className="am-route-item">
                  <span className="am-route-rank">{i + 1}</span>
                  <code className="am-route-name">{r.route}</code>
                  <span className="am-route-count">{r.count}×</span>
                </div>
              ))}
            </div>
          </section>
        )}

        {data?.requests?.length > 0 && (
          <section className="am-panel-section">
            <h4 className="am-panel-section-title">
              Histórico
              <span className="am-panel-section-badge">{data.requests.length}</span>
            </h4>
            <div className="am-mini-table-wrap">
              <table className="am-mini-table">
                <thead>
                  <tr><th>Rota</th><th>Mét.</th><th>St</th><th>Dur.</th><th>Quando</th></tr>
                </thead>
                <tbody>
                  {data.requests.slice(0, 100).map((r, i) => (
                    <tr key={i} className={r.statusCode >= 500 ? 'am-row-err' : ''}>
                      <td className="am-route-cell">{r.route}</td>
                      <td><span className="am-method">{r.method}</span></td>
                      <td>
                        <span className={r.statusCode >= 500 ? 'am-stat am-stat--err' : r.statusCode >= 400 ? 'am-stat am-stat--warn' : 'am-stat'}>
                          {r.statusCode}
                        </span>
                      </td>
                      <td className={r.duration > 2000 ? 'am-cell-danger' : ''}>{fmtMs(r.duration)}</td>
                      <td className="am-cell-meta">{fmtDate(r.timestamp)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        )}
      </aside>
    </>
  )
}

// ── Página principal ───────────────────────────────────────────────────────

export default function AdminMonitoringPage() {
  const qc = useQueryClient()

  // Filtros globais
  const [range, setRange] = useState('24h')
  const [hideLocalhost, setHideLocalhost] = useState(true)

  // Tabs principais
  const [tab, setTab] = useState('overview')
  // Tabs internas da Identidades
  const [idTab, setIdTab] = useState('actors')

  // Filtros / paginação por tab
  const [actorSearch, setActorSearch] = useState('')
  const [riskFilter, setRiskFilter] = useState('all')
  const [actorPage, setActorPage] = useState(1)
  const [routePage, setRoutePage] = useState(1)
  const [errPage, setErrPage] = useState(1)
  const [slowPage, setSlowPage] = useState(1)
  const [vitalsPage, setVitalsPage] = useState(1)
  const [selectedActor, setSelectedActor] = useState(null)
  const [expanded, setExpanded] = useState(new Set())

  const main = useMonitoring(range, hideLocalhost)
  const actorsQ = useActors(range, hideLocalhost)
  const blockedQ = useBlockedIPs()

  const overview = main.data?.overview
  const routes   = main.data?.routes   ?? []
  const errors   = main.data?.errors   ?? []
  const slow     = main.data?.slow     ?? []
  const timeline = main.data?.timeline ?? []
  const vitals   = main.data?.vitals   ?? []
  const actors   = actorsQ.data ?? []
  const blocked  = blockedQ.data ?? []

  const refetchAll = useCallback(() => {
    main.refetch(); actorsQ.refetch(); blockedQ.refetch()
  }, [main, actorsQ, blockedQ])

  // Resetar páginas ao mudar filtros globais.
  useEffect(() => {
    setActorPage(1); setRoutePage(1); setErrPage(1); setSlowPage(1); setVitalsPage(1)
  }, [range, hideLocalhost])

  // ── Block handlers ────────────────────────────────────────────────────
  const blockIP = useCallback(async (ip) => {
    const ok = await window.confirm(`Bloquear ${ip}? Todos os requests deste IP serão rejeitados com 403. Rotas /admin ficam isentas para o operador poder desbloquear.`)
    if (!ok) return
    try {
      await api.post('/admin/monitoring/block-ip', { ip, reason: 'Bloqueado via painel de monitoramento' })
      qc.invalidateQueries({ queryKey: ['admin-monitoring-actors'] })
      qc.invalidateQueries({ queryKey: ['admin-monitoring-blocked'] })
      setSelectedActor((prev) => prev?.ip === ip ? { ...prev, isIPBlocked: true } : prev)
    } catch {
      window.alert('Erro ao bloquear IP.')
    }
  }, [qc])

  const unblockIP = useCallback(async (ip) => {
    const ok = await window.confirm(`Desbloquear ${ip}? Requests deste endereço voltam a passar normalmente.`)
    if (!ok) return
    try {
      await api.delete(`/admin/monitoring/block-ip/${encodeURIComponent(ip)}`)
      qc.invalidateQueries({ queryKey: ['admin-monitoring-actors'] })
      qc.invalidateQueries({ queryKey: ['admin-monitoring-blocked'] })
      setSelectedActor((prev) => prev?.ip === ip ? { ...prev, isIPBlocked: false } : prev)
    } catch {
      window.alert('Erro ao desbloquear IP.')
    }
  }, [qc])

  const blockUser = useCallback(async (userId, email) => {
    const ok = await window.confirm(`Bloquear a conta de ${email || userId}? O usuário fica is_active=false e não poderá fazer novos logins.`)
    if (!ok) return
    try {
      await api.post(`/admin/monitoring/block-user/${userId}`)
      qc.invalidateQueries({ queryKey: ['admin-monitoring-actors'] })
    } catch {
      window.alert('Erro ao bloquear usuário.')
    }
  }, [qc])

  // ── Agregação Identidades: agrupa por usuário (múltiplos IPs → 1 linha) ──
  const groupedActors = useMemo(() => {
    const userMap = new Map()
    const anonList = []
    const riskOrder = ['low', 'medium', 'high', 'critical']
    for (const a of actors) {
      if (a.userId) {
        if (!userMap.has(a.userId)) {
          userMap.set(a.userId, {
            userId: a.userId,
            userEmail: a.userEmail,
            totalRequests: 0,
            uniqueRouteCount: 0,
            errorCount: 0,
            slowCount: 0,
            firstSeen: a.firstSeen,
            lastSeen: a.lastSeen,
            riskLevel: 'low',
            isIPBlocked: false,
            isGrouped: true,
            ips: [],
          })
        }
        const g = userMap.get(a.userId)
        g.totalRequests += a.totalRequests
        g.errorCount += a.errorCount
        g.slowCount += (a.slowCount || 0)
        g.uniqueRouteCount = Math.max(g.uniqueRouteCount, a.uniqueRouteCount)
        if (new Date(a.firstSeen) < new Date(g.firstSeen)) g.firstSeen = a.firstSeen
        if (new Date(a.lastSeen)  > new Date(g.lastSeen))  g.lastSeen  = a.lastSeen
        if (a.isIPBlocked) g.isIPBlocked = true
        if (riskOrder.indexOf(a.riskLevel) > riskOrder.indexOf(g.riskLevel)) g.riskLevel = a.riskLevel
        g.ips.push(a)
      } else {
        anonList.push({ ...a, isGrouped: false })
      }
    }
    return [...userMap.values(), ...anonList].sort((x, y) => y.totalRequests - x.totalRequests)
  }, [actors])

  const filteredActors = useMemo(() => {
    let list = groupedActors
    if (riskFilter !== 'all') list = list.filter((a) => a.riskLevel === riskFilter)
    if (actorSearch.trim()) {
      const q = actorSearch.toLowerCase()
      list = list.filter((a) =>
        a.ip?.toLowerCase().includes(q) ||
        a.userEmail?.toLowerCase().includes(q) ||
        (a.ips && a.ips.some((ip) => ip.ip?.toLowerCase().includes(q))),
      )
    }
    return list
  }, [groupedActors, riskFilter, actorSearch])

  const maxRequests = useMemo(
    () => filteredActors.reduce((m, a) => Math.max(m, a.totalRequests), 0),
    [filteredActors],
  )
  const actorSummary = useMemo(() => ({
    uniqueIPs: new Set(actors.map((a) => a.ip).filter(Boolean)).size,
    authenticated: groupedActors.filter((a) => a.userId).length,
    highRisk: groupedActors.filter((a) => a.riskLevel === 'high' || a.riskLevel === 'critical').length,
    blocked: actors.filter((a) => a.isIPBlocked).length,
  }), [actors, groupedActors])

  const tabs = [
    { id: 'overview',    label: 'Visão geral',   Icon: Icon.Timeline },
    { id: 'identidades', label: 'Identidades',   Icon: Icon.Shield,
      badge: actorSummary.highRisk > 0 ? actorSummary.highRisk : null, badgeDanger: true },
    { id: 'performance', label: 'Performance',   Icon: Icon.Speed },
    { id: 'vitals',      label: 'Web Vitals',    Icon: Icon.Web },
    { id: 'erros',       label: 'Erros',         Icon: Icon.Error,
      badge: (errors.length + slow.length) > 0 ? errors.length + slow.length : null, badgeDanger: true },
  ]

  const paginatedRoutes  = useMemo(() => routes.slice((routePage - 1) * PER_PAGE, routePage * PER_PAGE), [routes, routePage])
  const paginatedActors  = useMemo(() => filteredActors.slice((actorPage - 1) * PER_PAGE, actorPage * PER_PAGE), [filteredActors, actorPage])
  const paginatedErrors  = useMemo(() => errors.slice((errPage - 1) * PER_PAGE, errPage * PER_PAGE), [errors, errPage])
  const paginatedSlow    = useMemo(() => slow.slice((slowPage - 1) * PER_PAGE, slowPage * PER_PAGE), [slow, slowPage])
  const paginatedVitals  = useMemo(() => vitals.slice((vitalsPage - 1) * PER_PAGE, vitalsPage * PER_PAGE), [vitals, vitalsPage])

  // ── Render ────────────────────────────────────────────────────────────
  return (
    <div className="am">
      {/* Page header */}
      <header className="am-header">
        <div className="am-title-row">
          <div className="am-title-icon"><Icon.Monitor /></div>
          <div>
            <h1 className="am-title">Monitoramento</h1>
            <p className="am-subtitle">Performance, identidades e Web Vitals da plataforma</p>
          </div>
        </div>
        <div className="am-controls">
          <div className="am-range">
            {RANGES.map((r) => (
              <button
                key={r.value}
                type="button"
                className={`am-range-btn ${range === r.value ? 'am-range-btn--active' : ''}`}
                onClick={() => setRange(r.value)}
              >{r.label}</button>
            ))}
          </div>
          <button
            type="button"
            className={`am-toggle ${hideLocalhost ? 'am-toggle--active' : ''}`}
            onClick={() => setHideLocalhost((v) => !v)}
            title={hideLocalhost ? 'Incluir localhost' : 'Excluir localhost'}
          >
            {hideLocalhost ? 'Sem localhost' : 'Com localhost'}
          </button>
          <button
            type="button"
            className="am-refresh"
            onClick={refetchAll}
            disabled={main.isFetching}
            aria-label="Atualizar"
            title="Atualizar"
          >
            <span className={main.isFetching ? 'am-spin' : ''}><Icon.Refresh /></span>
          </button>
        </div>
      </header>

      {main.isError && (
        <div className="am-banner">
          <Icon.Error />
          <span>Erro ao carregar dados. Pode ser problema de rede ou sessão expirada.</span>
          <button type="button" onClick={() => main.refetch()}>Tentar novamente</button>
        </div>
      )}

      {/* Tab bar */}
      <nav className="am-tabs">
        {tabs.map(({ id, label, Icon: I, badge, badgeDanger }) => (
          <button
            key={id}
            type="button"
            className={`am-tab ${tab === id ? 'am-tab--active' : ''}`}
            onClick={() => setTab(id)}
          >
            <I />
            <span>{label}</span>
            {badge != null && (
              <span className={`am-tab-badge ${badgeDanger ? 'am-tab-badge--danger' : ''}`}>{badge}</span>
            )}
          </button>
        ))}
      </nav>

      {/* ── TAB: Visão geral ───────────────────────────────────────────── */}
      {tab === 'overview' && (
        <div className="am-tab-content" key="overview">
          {main.isLoading ? (
            <KpiSkeleton />
          ) : overview ? (
            <>
              <div className="am-kpi-grid">
                <article className="am-kpi am-kpi--hero">
                  <span className="am-kpi-label">Total de requests</span>
                  <span className="am-kpi-value">{fmtNum(overview.period.totalRequests)}</span>
                  <span className="am-kpi-meta">no período selecionado</span>
                </article>

                <article className={`am-kpi ${overview.period.totalErrors > 0 ? 'am-kpi--alert' : ''}`}>
                  <span className="am-kpi-icon am-kpi-icon--err"><Icon.Error /></span>
                  <span className="am-kpi-label">Erros 5xx</span>
                  <span className="am-kpi-value">{fmtNum(overview.period.totalErrors)}</span>
                  <span className="am-kpi-meta">{overview.period.errorRate} do total</span>
                </article>

                <article className={`am-kpi ${overview.period.totalSlow > 0 ? 'am-kpi--warn' : ''}`}>
                  <span className="am-kpi-icon am-kpi-icon--warn"><Icon.Timer /></span>
                  <span className="am-kpi-label">Requests lentos</span>
                  <span className="am-kpi-value">{fmtNum(overview.period.totalSlow)}</span>
                  <span className="am-kpi-meta">acima de 2 segundos</span>
                </article>

                <article className="am-kpi">
                  <span className="am-kpi-icon am-kpi-icon--ok"><Icon.Timeline /></span>
                  <span className="am-kpi-label">Latência média</span>
                  <span className="am-kpi-value">{fmtMs(overview.period.avgDuration)}</span>
                  <span className="am-kpi-meta">tempo de resposta médio</span>
                </article>
              </div>

              <div className="am-server-strip">
                <span className="am-strip-item"><Icon.Server /> Uptime <strong>{overview.server.uptime.human}</strong></span>
                <span className="am-strip-divider" />
                <span className="am-strip-item"><Icon.Memory /> Heap <strong>{overview.server.memory.heapUsedMB}MB</strong> / {overview.server.memory.heapTotalMB}MB</span>
                <span className="am-strip-divider" />
                <span className="am-strip-item">RSS <strong>{overview.server.memory.rssMB}MB</strong></span>
                <span className="am-strip-divider" />
                <span className="am-strip-item">Runtime <strong>{overview.server.node}</strong></span>
              </div>
            </>
          ) : null}

          <section className="am-card">
            <header className="am-card-head">
              <span className="am-card-head-icon"><Icon.Timeline /></span>
              <h2 className="am-card-title">Volume de requests</h2>
            </header>
            {main.isLoading ? (
              <div className="am-skel" style={{ height: 240 }} />
            ) : timeline.length === 0 ? (
              <EmptyState icon={<Icon.Timeline />} title="Nenhum dado de timeline ainda" hint="Vai acumular conforme requests chegam" />
            ) : (
              <TimelineChart timeline={timeline} />
            )}
          </section>
        </div>
      )}

      {/* ── TAB: Identidades ──────────────────────────────────────────── */}
      {tab === 'identidades' && (
        <div className="am-tab-content" key="identidades">
          <section className="am-card am-card--identities">
            <header className="am-card-head am-card-head--id">
              <div className="am-id-title">
                <span className="am-card-head-icon"><Icon.Shield /></span>
                <div>
                  <h2 className="am-card-title">Identidades &amp; acesso</h2>
                  <p className="am-card-sub">Quem está acessando — por IP e usuário, com risco calculado automaticamente</p>
                </div>
              </div>
              <div className="am-id-tabs">
                {[
                  { id: 'actors',  label: 'Atores',           count: actors.length },
                  { id: 'blocked', label: 'IPs bloqueados',   count: blocked.length, danger: true },
                ].map((t) => (
                  <button
                    key={t.id}
                    type="button"
                    className={`am-id-tab ${idTab === t.id ? 'am-id-tab--active' : ''}`}
                    onClick={() => setIdTab(t.id)}
                  >
                    {t.label}
                    {t.count > 0 && (
                      <span className={`am-tab-badge ${t.danger ? 'am-tab-badge--danger' : ''}`}>{t.count}</span>
                    )}
                  </button>
                ))}
              </div>
            </header>

            {idTab === 'actors' && (
              <>
                <div className="am-actor-kpis">
                  {[
                    { value: actorSummary.uniqueIPs,     label: 'IPs únicos' },
                    { value: actorSummary.authenticated, label: 'Autenticados' },
                    { value: actorSummary.highRisk,      label: 'Alto risco',     danger: actorSummary.highRisk > 0 },
                    { value: actorSummary.blocked,       label: 'IPs bloqueados', warn:   actorSummary.blocked > 0 },
                  ].map((k, i, arr) => (
                    <span key={i} className="am-actor-kpi-cell">
                      <span className={`am-actor-kpi-value ${k.danger ? 'am-actor-kpi-value--danger' : k.warn ? 'am-actor-kpi-value--warn' : ''}`}>{k.value}</span>
                      <span className="am-actor-kpi-label">{k.label}</span>
                      {i < arr.length - 1 && <span className="am-actor-kpi-divider" />}
                    </span>
                  ))}
                </div>

                <div className="am-toolbar">
                  <div className="am-search">
                    <span className="am-search-icon"><Icon.Search /></span>
                    <input
                      className="am-search-input"
                      type="text"
                      value={actorSearch}
                      onChange={(e) => { setActorSearch(e.target.value); setActorPage(1) }}
                      placeholder="Buscar por IP ou e-mail…"
                    />
                    {actorSearch && (
                      <button type="button" className="am-search-clear" onClick={() => setActorSearch('')}><Icon.Close /></button>
                    )}
                  </div>
                  <div className="am-risk-filter">
                    <span className="am-risk-filter-icon"><Icon.Filter /></span>
                    {['all', 'critical', 'high', 'medium', 'low'].map((r) => (
                      <button
                        key={r}
                        type="button"
                        className={`am-risk-btn ${riskFilter === r ? 'am-risk-btn--active' : ''} ${r !== 'all' ? `am-risk-btn--${r}` : ''}`}
                        onClick={() => { setRiskFilter(r); setActorPage(1) }}
                      >{r === 'all' ? 'Todos' : RISK_LABELS[r]}</button>
                    ))}
                  </div>
                </div>

                {actorsQ.isLoading ? (
                  <ActorTableSkeleton />
                ) : filteredActors.length === 0 ? (
                  <EmptyState icon={<Icon.Shield />}
                    title={actors.length === 0 ? 'Nenhum dado de identidade ainda' : 'Sem resultados para este filtro'}
                    hint={actors.length === 0 ? 'Vai acumular conforme requests chegam' : 'Tente ajustar os filtros ou a busca'}
                  />
                ) : (
                  <div className="am-table-wrap">
                    <table className="am-table am-table--actors">
                      <thead>
                        <tr>
                          <th>IP</th>
                          <th>Usuário</th>
                          <ThTip tip="Total de requests deste ator no período. A barra mostra volume relativo.">Requests</ThTip>
                          <ThTip tip="Rotas distintas que esse ator acessou.">Rotas</ThTip>
                          <ThTip tip="Respostas 5xx geradas por esse ator.">Erros</ThTip>
                          <ThTip tip="Baseado em % de 404s, diversidade de rotas e se está autenticado. Crítico = provável bot.">Risco</ThTip>
                          <th>Último acesso</th>
                          <th></th>
                        </tr>
                      </thead>
                      <tbody>
                        {paginatedActors.flatMap((a, i) => {
                          const isGroup = a.isGrouped
                          const isExp = isGroup && expanded.has(a.userId)
                          const multi = isGroup && a.ips && a.ips.length > 1
                          const isSel = isGroup
                            ? (selectedActor?.userId === a.userId && !selectedActor?.ip)
                            : (selectedActor?.ip === a.ip)
                          const main = (
                            <tr key={`a-${i}`} className={`${a.isIPBlocked ? 'am-row-blocked' : ''} ${isSel ? 'am-row-sel' : ''}`}>
                              <td>
                                {isGroup ? (
                                  <div className="am-ip-cell">
                                    <button
                                      type="button"
                                      className="am-expand"
                                      onClick={() => multi && setExpanded((prev) => {
                                        const next = new Set(prev)
                                        if (next.has(a.userId)) next.delete(a.userId); else next.add(a.userId)
                                        return next
                                      })}
                                      style={{ visibility: multi ? 'visible' : 'hidden' }}
                                      aria-label={isExp ? 'Recolher' : 'Expandir'}
                                    >
                                      {isExp ? <Icon.ChevDown /> : <Icon.ChevRight />}
                                    </button>
                                    <span className="am-ip-group">{a.ips.length} {a.ips.length === 1 ? 'IP' : 'IPs'}</span>
                                    {a.isIPBlocked && <span className="am-blocked-tag"><Icon.Block /> Bloqueado</span>}
                                  </div>
                                ) : (
                                  <div className="am-ip-cell">
                                    <code className="am-ip">{a.ip}</code>
                                    <CopyBtn text={a.ip} />
                                    {a.isIPBlocked && <span className="am-blocked-tag"><Icon.Block /> Bloqueado</span>}
                                  </div>
                                )}
                              </td>
                              <td>
                                {a.userEmail ? (
                                  <div className="am-user-cell">
                                    <div className="am-user-avatar">{a.userEmail.charAt(0).toUpperCase()}</div>
                                    <span className="am-user-email">{a.userEmail}</span>
                                  </div>
                                ) : (
                                  <span className="am-anon"><Icon.Anon /> Anônimo</span>
                                )}
                              </td>
                              <td><RequestBar count={a.totalRequests} max={maxRequests} /></td>
                              <td className="am-num">{a.uniqueRouteCount}</td>
                              <td className="am-num">
                                {a.errorCount > 0
                                  ? <span className="am-err-count">{a.errorCount}</span>
                                  : <span className="am-zero">0</span>}
                              </td>
                              <td><RiskBadge level={a.riskLevel} /></td>
                              <td className="am-cell-meta">{fmtDate(a.lastSeen)}</td>
                              <td>
                                <button type="button" className="am-detail-btn" onClick={() => setSelectedActor(a)}>
                                  <Icon.Open /> Detalhes
                                </button>
                              </td>
                            </tr>
                          )
                          if (!isExp) return [main]
                          const subs = a.ips.map((ip, j) => (
                            <tr key={`a-${i}-${j}`} className={`am-sub-row ${ip.isIPBlocked ? 'am-row-blocked' : ''} ${selectedActor?.ip === ip.ip ? 'am-row-sel' : ''}`}>
                              <td>
                                <div className="am-ip-cell am-ip-cell--sub">
                                  <span className="am-sub-indent" />
                                  <code className="am-ip">{ip.ip}</code>
                                  <CopyBtn text={ip.ip} />
                                  {ip.isIPBlocked && <span className="am-blocked-tag"><Icon.Block /> Bloqueado</span>}
                                </div>
                              </td>
                              <td />
                              <td><RequestBar count={ip.totalRequests} max={maxRequests} /></td>
                              <td className="am-num">{ip.uniqueRouteCount}</td>
                              <td className="am-num">
                                {ip.errorCount > 0
                                  ? <span className="am-err-count">{ip.errorCount}</span>
                                  : <span className="am-zero">0</span>}
                              </td>
                              <td><RiskBadge level={ip.riskLevel} /></td>
                              <td className="am-cell-meta">{fmtDate(ip.lastSeen)}</td>
                              <td>
                                <button type="button" className="am-detail-btn" onClick={() => setSelectedActor(ip)}>
                                  <Icon.Open /> Detalhes
                                </button>
                              </td>
                            </tr>
                          ))
                          return [main, ...subs]
                        })}
                      </tbody>
                    </table>
                    <Pagination
                      page={actorPage}
                      total={Math.max(1, Math.ceil(filteredActors.length / PER_PAGE))}
                      onChange={setActorPage}
                    />
                  </div>
                )}
              </>
            )}

            {idTab === 'blocked' && (
              blockedQ.isLoading ? (
                <div className="am-loading">Carregando…</div>
              ) : blocked.length === 0 ? (
                <EmptyState icon={<Icon.Shield />} title="Nenhum IP bloqueado" hint="Bloqueie IPs suspeitos na aba Atores" />
              ) : (
                <div className="am-table-wrap">
                  <table className="am-table">
                    <thead>
                      <tr><th>IP</th><th>Motivo</th><th>Por</th><th>Data</th><th></th></tr>
                    </thead>
                    <tbody>
                      {blocked.map((b) => (
                        <tr key={b.ip}>
                          <td>
                            <div className="am-ip-cell">
                              <span className="am-blocked-icon"><Icon.Block /></span>
                              <code className="am-ip">{b.ip}</code>
                              <CopyBtn text={b.ip} />
                            </div>
                          </td>
                          <td className="am-cell-meta">{b.reason || '—'}</td>
                          <td className="am-cell-meta">{b.blockedByEmail || '—'}</td>
                          <td className="am-cell-meta">{new Date(b.blockedAt).toLocaleString('pt-BR', { day: '2-digit', month: '2-digit', year: '2-digit', hour: '2-digit', minute: '2-digit' })}</td>
                          <td>
                            <button type="button" className="am-detail-btn am-detail-btn--unblock" onClick={() => unblockIP(b.ip)}>
                              <Icon.Unlock /> Desbloquear
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )
            )}
          </section>
        </div>
      )}

      {/* ── TAB: Performance ──────────────────────────────────────────── */}
      {tab === 'performance' && (
        <div className="am-tab-content" key="performance">
          <section className="am-card">
            <header className="am-card-head">
              <span className="am-card-head-icon"><Icon.Speed /></span>
              <h2 className="am-card-title">Latência por rota <span className="am-pill">{routes.length}</span></h2>
            </header>
            {main.isLoading ? (
              <TableSkeleton rows={8} cols={8} />
            ) : routes.length === 0 ? (
              <EmptyState icon={<Icon.Speed />} title="Nenhuma métrica de rota ainda" hint="Dados aparecem conforme requests chegam" />
            ) : (
              <div className="am-table-wrap">
                <table className="am-table">
                  <thead>
                    <tr>
                      <th>Rota</th>
                      <ThTip tip="Total de chamadas no período">Requests</ThTip>
                      <ThTip tip="p50 — mediana, experiência típica">p50</ThTip>
                      <ThTip tip="p95 — 95% dos requests ficam abaixo deste tempo. Referência de saúde da rota.">p95</ThTip>
                      <ThTip tip="p99 — pior caso real">p99</ThTip>
                      <ThTip tip="Tempo médio de resposta">Média</ThTip>
                      <ThTip tip="Respostas com status 500+">Erros</ThTip>
                      <ThTip tip="Good: p95 < 500ms + erro < 1% | Warning: p95 < 2s + erro < 5% | Critical: acima">Status</ThTip>
                    </tr>
                  </thead>
                  <tbody>
                    {paginatedRoutes.map((r) => (
                      <tr key={r.route}>
                        <td className="am-route-cell">{r.route}</td>
                        <td>{r.count}</td>
                        <td>{fmtMs(r.p50)}</td>
                        <td className={r.p95 > 500 ? 'am-cell-warn' : ''}>{fmtMs(r.p95)}</td>
                        <td className={r.p99 > 2000 ? 'am-cell-danger' : ''}>{fmtMs(r.p99)}</td>
                        <td>{fmtMs(r.avg)}</td>
                        <td>{r.errorCount > 0
                          ? <span className="am-err-count">{r.errorCount} ({r.errorRate})</span>
                          : <span className="am-zero">0</span>}</td>
                        <td><HealthBadge health={r.health} /></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                <Pagination
                  page={routePage}
                  total={Math.max(1, Math.ceil(routes.length / PER_PAGE))}
                  onChange={setRoutePage}
                />
              </div>
            )}
          </section>
        </div>
      )}

      {/* ── TAB: Web Vitals ───────────────────────────────────────────── */}
      {tab === 'vitals' && (
        <div className="am-tab-content" key="vitals">
          <section className="am-card">
            <header className="am-card-head">
              <span className="am-card-head-icon"><Icon.Web /></span>
              <h2 className="am-card-title">Web Vitals <span className="am-pill">{vitals.length}</span></h2>
            </header>
            {vitals.length === 0 ? (
              <EmptyState
                icon={<Icon.Web />}
                title="Nenhum Web Vital coletado ainda"
                hint="A coleta automática no frontend não está instalada. Veja docs/features/admin-monitoring.md."
              />
            ) : (
              <div className="am-table-wrap">
                <table className="am-table">
                  <thead>
                    <tr>
                      <ThTip tip="Core Web Vitals — métricas do Google que medem experiência real">Métrica</ThTip>
                      <th>Página</th>
                      <ThTip tip="Número de amostras">Amostras</ThTip>
                      <ThTip tip="Valor médio">Média</ThTip>
                      <ThTip tip="Percentil 75 — referência do Google">p75</ThTip>
                      <ThTip tip="% classificada como 'boa'">Bom</ThTip>
                      <ThTip tip="% classificada como 'ruim'">Ruim</ThTip>
                    </tr>
                  </thead>
                  <tbody>
                    {paginatedVitals.map((v, i) => (
                      <tr key={`${v.name}-${v.page}-${i}`}>
                        <td>
                          <span className="am-th-tip">
                            <strong>{v.name}</strong>
                            <TipIcon tip={VITAL_DESC[v.name] ?? ''} />
                          </span>
                        </td>
                        <td className="am-route-cell">{v.page || '—'}</td>
                        <td>{v.count}</td>
                        <td>{v.name === 'CLS' ? v.avg.toFixed(3) : fmtMs(Math.round(v.avg))}</td>
                        <td>{v.name === 'CLS' ? v.p75.toFixed(3) : fmtMs(Math.round(v.p75))}</td>
                        <td><span className="am-rating-good">{v.goodPercent}</span></td>
                        <td><span className={v.poorCount > 0 ? 'am-rating-poor' : ''}>{v.poorPercent}</span></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                <Pagination
                  page={vitalsPage}
                  total={Math.max(1, Math.ceil(vitals.length / PER_PAGE))}
                  onChange={setVitalsPage}
                />
              </div>
            )}
          </section>
        </div>
      )}

      {/* ── TAB: Erros ─────────────────────────────────────────────────── */}
      {tab === 'erros' && (
        <div className="am-tab-content" key="erros">
          <section className="am-card">
            <header className="am-card-head">
              <span className="am-card-head-icon am-card-head-icon--err"><Icon.Error /></span>
              <h2 className="am-card-title">Top erros 5xx <span className="am-pill">{errors.length}</span></h2>
            </header>
            {errors.length === 0 ? (
              <EmptyState ok icon={<Icon.Check />} title="Nenhum erro 5xx registrado" hint="Ótimo sinal — o servidor está respondendo bem" />
            ) : (
              <div className="am-table-wrap">
                <table className="am-table">
                  <thead>
                    <tr>
                      <th>Rota</th>
                      <th>Status</th>
                      <th>Ocorrências</th>
                      <th>Última vez</th>
                      <th>Latência média</th>
                    </tr>
                  </thead>
                  <tbody>
                    {paginatedErrors.map((e, i) => (
                      <tr key={i}>
                        <td className="am-route-cell">{e.route}</td>
                        <td><span className="am-stat am-stat--err">{e.statusCode}</span></td>
                        <td><strong>{e.count}</strong></td>
                        <td className="am-cell-meta">{fmtDate(e.lastOccurrence)}</td>
                        <td>{fmtMs(e.avgDuration)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                <Pagination
                  page={errPage}
                  total={Math.max(1, Math.ceil(errors.length / PER_PAGE))}
                  onChange={setErrPage}
                />
              </div>
            )}
          </section>

          <section className="am-card">
            <header className="am-card-head">
              <span className="am-card-head-icon am-card-head-icon--warn"><Icon.Timer /></span>
              <h2 className="am-card-title">Requests lentos &gt;2s <span className="am-pill">{slow.length}</span></h2>
            </header>
            {slow.length === 0 ? (
              <EmptyState ok icon={<Icon.Check />} title="Nenhum request lento" hint="Tudo respondendo abaixo de 2 segundos" />
            ) : (
              <div className="am-table-wrap">
                <table className="am-table">
                  <thead>
                    <tr>
                      <th>Rota</th>
                      <th>Método</th>
                      <th>Status</th>
                      <th>Duração</th>
                      <th>Quando</th>
                    </tr>
                  </thead>
                  <tbody>
                    {paginatedSlow.map((r, i) => (
                      <tr key={i}>
                        <td className="am-route-cell">{r.route}</td>
                        <td><span className="am-method">{r.method}</span></td>
                        <td>{r.statusCode}</td>
                        <td className="am-cell-danger"><strong>{fmtMs(r.duration)}</strong></td>
                        <td className="am-cell-meta">{fmtDate(r.timestamp)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                <Pagination
                  page={slowPage}
                  total={Math.max(1, Math.ceil(slow.length / PER_PAGE))}
                  onChange={setSlowPage}
                />
              </div>
            )}
          </section>
        </div>
      )}

      {selectedActor && (
        <ActorDetailPanel
          actor={selectedActor}
          range={range}
          onClose={() => setSelectedActor(null)}
          onBlockIp={blockIP}
          onBlockUser={blockUser}
          onUnblockIp={unblockIP}
        />
      )}
    </div>
  )
}

// ── Sub-componentes locais ─────────────────────────────────────────────────

function EmptyState({ icon, title, hint, ok }) {
  return (
    <div className={`am-empty ${ok ? 'am-empty--ok' : ''}`}>
      <div className="am-empty-icon">{icon}</div>
      <p className="am-empty-title">{title}</p>
      {hint && <p className="am-empty-hint">{hint}</p>}
    </div>
  )
}

function KpiSkeleton() {
  return (
    <div className="am-kpi-grid">
      {[0, 1, 2, 3].map((i) => (
        <div key={i} className={`am-kpi am-kpi--skel ${i === 0 ? 'am-kpi--hero am-kpi--hero-skel' : ''}`}>
          <div className="am-skel" style={{ width: '40%', height: 11 }} />
          <div className="am-skel" style={{ width: '60%', height: 28, marginTop: 8 }} />
          <div className="am-skel" style={{ width: '50%', height: 10, marginTop: 6 }} />
        </div>
      ))}
    </div>
  )
}

function TableSkeleton({ rows, cols }) {
  return (
    <div className="am-table-skel">
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="am-table-skel-row">
          {Array.from({ length: cols }).map((__, j) => (
            <div key={j} className="am-skel" style={{ flex: j === 0 ? 3 : 1, height: 12 }} />
          ))}
        </div>
      ))}
    </div>
  )
}

function ActorTableSkeleton() {
  return (
    <div className="am-table-skel">
      {Array.from({ length: 6 }).map((_, i) => (
        <div key={i} className="am-table-skel-row">
          <div className="am-skel" style={{ width: '22%', height: 12 }} />
          <div className="am-skel" style={{ width: '24%', height: 12 }} />
          <div className="am-skel" style={{ width: '18%', height: 12 }} />
          <div className="am-skel" style={{ width: '10%', height: 18, borderRadius: 9999 }} />
          <div className="am-skel" style={{ width: '14%', height: 12 }} />
          <div className="am-skel" style={{ width: '10%', height: 24, borderRadius: 6 }} />
        </div>
      ))}
    </div>
  )
}

// TimelineChart: três SVG paths superpostos — requests (área rosa), erros
// (linha vermelha), lentos (linha âmbar). Eixos implícitos.
function TimelineChart({ timeline }) {
  const maxV = Math.max(1, ...timeline.map((t) => t.totalRequests))
  const W = 100, H = 100
  const N = timeline.length
  const xs = (i) => N <= 1 ? W / 2 : (i / (N - 1)) * W
  const ys = (v) => H - (v / maxV) * (H * 0.92)

  const reqArea = (() => {
    if (N === 0) return ''
    let d = `M0,${H} `
    timeline.forEach((t, i) => { d += `L${xs(i).toFixed(2)},${ys(t.totalRequests).toFixed(2)} ` })
    d += `L${W},${H} Z`
    return d
  })()

  const reqLine = timeline.map((t, i) => `${i === 0 ? 'M' : 'L'}${xs(i).toFixed(2)},${ys(t.totalRequests).toFixed(2)}`).join(' ')
  const errLine = timeline.map((t, i) => `${i === 0 ? 'M' : 'L'}${xs(i).toFixed(2)},${ys(t.totalErrors).toFixed(2)}`).join(' ')
  const sloLine = timeline.map((t, i) => `${i === 0 ? 'M' : 'L'}${xs(i).toFixed(2)},${ys(t.totalSlow).toFixed(2)}`).join(' ')

  return (
    <div className="am-timeline">
      <svg className="am-timeline-svg" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none">
        <path d={reqArea} fill="rgba(232,30,117,0.08)" />
        <path d={reqLine} fill="none" stroke="var(--c-action)"   strokeWidth="1.2" />
        <path d={sloLine} fill="none" stroke="var(--c-warning)" strokeWidth="0.8" />
        <path d={errLine} fill="none" stroke="var(--c-danger)"  strokeWidth="0.8" />
      </svg>
      <div className="am-timeline-legend">
        <span><span className="am-legend-swatch" style={{ background: 'var(--c-action)' }} /> Requests</span>
        <span><span className="am-legend-swatch" style={{ background: 'var(--c-warning)' }} /> Lentos</span>
        <span><span className="am-legend-swatch" style={{ background: 'var(--c-danger)' }} /> Erros</span>
      </div>
    </div>
  )
}
