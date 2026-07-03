import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { createPortal } from 'react-dom'
import api from '../api/client'
import { buildJourney } from '../utils/userJourney'
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
  // ── Jornada — ícone da aba + ícones por categoria de ação ──────────────────
  Route:   () => <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="4" cy="3.5" r="1.8"/><circle cx="12" cy="12.5" r="1.8"/><path d="M4 5.3v3.2a2.5 2.5 0 0 0 2.5 2.5h3a2.5 2.5 0 0 0 0 0"/><path d="M4 8.5h4.5a2.5 2.5 0 0 1 2.5 2.5v.7"/></svg>,
  Login:   () => <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M9 2.5h3.5v11H9"/><path d="M2.5 8h7M7 5.5 9.5 8 7 10.5"/></svg>,
  Eye:     () => <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M1.5 8S4 3.5 8 3.5 14.5 8 14.5 8 12 12.5 8 12.5 1.5 8 1.5 8Z"/><circle cx="8" cy="8" r="1.8"/></svg>,
  Plus:    () => <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><path d="M8 3v10M3 8h10"/></svg>,
  Pencil:  () => <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M11 2.5 13.5 5 6 12.5l-3 .5.5-3L11 2.5Z"/></svg>,
  Trash:   () => <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M3 4.5h10M6 4.5V3h4v1.5M4.5 4.5 5 13h6l.5-8.5M6.5 7v3.5M9.5 7v3.5"/></svg>,
  Download:() => <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M8 2.5v7M5 7l3 2.5L11 7M3 12.5h10"/></svg>,
  Wave:    () => <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M2 8h1.5M5 5v6M8 3v10M11 5.5v5M14 8h-1.5"/></svg>,
  Sliders: () => <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M3 4.5h6M11.5 4.5h1.5M3 11.5h1.5M7 11.5h6"/><circle cx="10" cy="4.5" r="1.5"/><circle cx="5" cy="11.5" r="1.5"/></svg>,
  Clock:   () => <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="8" cy="8" r="6"/><path d="M8 4.5V8l2.5 1.5"/></svg>,
}

// Categoria de ação → ícone + rótulo curto. Usado na timeline da jornada.
const CAT_ICON = {
  session: Icon.Login, navigate: Icon.Eye, create: Icon.Plus, update: Icon.Pencil,
  destructive: Icon.Trash, export: Icon.Download, evidence: Icon.Wave, admin: Icon.Sliders, other: Icon.Eye,
}
const CAT_LABEL = {
  session: 'Sessão', navigate: 'Navegação', create: 'Criação', update: 'Edição',
  destructive: 'Sensível', export: 'Exportação', evidence: 'Evidência', admin: 'Admin', other: 'Ação',
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

// useJourney — busca o histórico de requests de UM usuário (por userId, todos os
// IPs) e reusa o mesmo endpoint /actor-detail do painel de identidades. A
// transformação em jornada legível é feita no componente via buildJourney().
function useJourney(userId, range) {
  return useQuery({
    queryKey: ['admin-monitoring-journey', userId, range],
    queryFn: async () => {
      const params = new URLSearchParams({ range })
      params.set('userId', userId)
      return (await api.get(`/admin/monitoring/actor-detail?${params}`)).data
    },
    enabled: !!userId,
    refetchInterval: 20_000,
    staleTime: 5_000,
  })
}

// ── Actor Detail Panel (slide-in) ──────────────────────────────────────────

function ActorDetailPanel({ actor, range, onClose, onBlockIp, onBlockUser, onUnblockIp, onViewJourney }) {
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
          {actor.userId && onViewJourney && (
            <button type="button" className="am-action-btn am-action-btn--journey" onClick={() => onViewJourney(actor)}>
              <Icon.Route /> Ver jornada
            </button>
          )}
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

// ── Jornada do usuário ──────────────────────────────────────────────────────
// Traduz o histórico cru de requests de um usuário (endpoint /actor-detail) numa
// narrativa cronológica legível: sessões, ações em português, horários, com
// destaque para erros e ações sensíveis. Zero backend novo — ver userJourney.js.

function fmtClock(d) {
  if (!d) return '—'
  return new Date(d).toLocaleTimeString('pt-BR', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}
function fmtClockMin(d) {
  if (!d) return '—'
  return new Date(d).toLocaleTimeString('pt-BR', { hour: '2-digit', minute: '2-digit' })
}
function fmtDur(ms) {
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 60) return `${s}s`
  const m = Math.round(s / 60)
  if (m < 60) return `${m} min`
  const h = Math.floor(m / 60), rm = m % 60
  return rm ? `${h}h ${rm}min` : `${h}h`
}
function fmtGap(ms) {
  const m = Math.round(ms / 60000)
  if (m < 60) return `${m} min depois`
  const h = Math.round(m / 60)
  if (h < 24) return `${h} h depois`
  const d = Math.round(h / 24)
  return `${d} ${d === 1 ? 'dia' : 'dias'} depois`
}
function dayLabel(d) {
  return new Date(d).toLocaleDateString('pt-BR', { day: '2-digit', month: '2-digit' })
}

function JourneyTab({ users, loadingUsers, selected, onSelect, search, onSearch, range, onBlockUser }) {
  return (
    <section className="am-card am-jn">
      <header className="am-card-head am-card-head--id">
        <div className="am-id-title">
          <span className="am-card-head-icon"><Icon.Route /></span>
          <div>
            <h2 className="am-card-title">Jornada do usuário</h2>
            <p className="am-card-sub">O passo a passo de cada pessoa na plataforma — telas abertas, ações e horários, traduzidos das chamadas de API</p>
          </div>
        </div>
      </header>

      <div className="am-jn-body">
        <JourneyRail
          users={users}
          loading={loadingUsers}
          selected={selected}
          onSelect={onSelect}
          search={search}
          onSearch={onSearch}
        />
        <JourneyPane selected={selected} range={range} onBlockUser={onBlockUser} />
      </div>
    </section>
  )
}

function JourneyRail({ users, loading, selected, onSelect, search, onSearch }) {
  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return users
    return users.filter((u) => (u.userEmail || '').toLowerCase().includes(q))
  }, [users, search])

  return (
    <aside className="am-jn-rail">
      <div className="am-jn-rail-head">
        <span className="am-jn-rail-title">Usuários</span>
        <span className="am-jn-rail-count">{users.length}</span>
      </div>
      <div className="am-search am-jn-search">
        <span className="am-search-icon"><Icon.Search /></span>
        <input
          className="am-search-input"
          type="text"
          value={search}
          onChange={(e) => onSearch(e.target.value)}
          placeholder="Buscar por e-mail…"
          aria-label="Buscar usuário"
        />
        {search && (
          <button type="button" className="am-search-clear" onClick={() => onSearch('')} aria-label="Limpar busca"><Icon.Close /></button>
        )}
      </div>

      <div className="am-jn-user-list" role="listbox" aria-label="Usuários">
        {loading ? (
          Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="am-jn-user am-jn-user--skel">
              <div className="am-skel" style={{ width: 30, height: 30, borderRadius: 9999 }} />
              <div style={{ flex: 1 }}>
                <div className="am-skel" style={{ width: '70%', height: 11 }} />
                <div className="am-skel" style={{ width: '40%', height: 9, marginTop: 6 }} />
              </div>
            </div>
          ))
        ) : filtered.length === 0 ? (
          <div className="am-jn-rail-empty">
            {users.length === 0 ? 'Nenhum usuário autenticado no período.' : 'Nenhum usuário para esta busca.'}
          </div>
        ) : (
          filtered.map((u) => {
            const isSel = selected?.userId === u.userId
            return (
              <button
                key={u.userId}
                type="button"
                role="option"
                aria-selected={isSel}
                className={`am-jn-user ${isSel ? 'am-jn-user--sel' : ''}`}
                onClick={() => onSelect({ userId: u.userId, userEmail: u.userEmail || null })}
              >
                <span className="am-jn-user-avatar">{(u.userEmail || '?').charAt(0).toUpperCase()}</span>
                <span className="am-jn-user-meta">
                  <span className="am-jn-user-email">{u.userEmail || 'Sem e-mail'}</span>
                  <span className="am-jn-user-sub">
                    <span className={`am-jn-risk-dot am-jn-risk-dot--${u.riskLevel}`} />
                    {fmtNum(u.totalRequests)} req · {fmtDate(u.lastSeen)}
                  </span>
                </span>
                <span className="am-jn-user-chev"><Icon.ChevRight /></span>
              </button>
            )
          })
        )}
      </div>
    </aside>
  )
}

function JourneyPane({ selected, range, onBlockUser }) {
  const q = useJourney(selected?.userId, range)
  const journey = useMemo(
    () => (q.data ? buildJourney(q.data.requests || [], {}) : null),
    [q.data],
  )

  if (!selected) {
    return (
      <div className="am-jn-pane am-jn-pane--empty">
        <JourneyGhost />
      </div>
    )
  }

  return (
    <div className="am-jn-pane">
      <header className="am-jn-phead">
        <div className="am-jn-phead-id">
          <span className="am-jn-phead-avatar">{(selected.userEmail || '?').charAt(0).toUpperCase()}</span>
          <div className="am-jn-phead-meta">
            <span className="am-jn-phead-email">{selected.userEmail || 'Usuário sem e-mail'}</span>
            {journey && journey.summary.actionCount > 0 && (
              <span className="am-jn-phead-period">
                <Icon.Clock /> {fmtDateSec(journey.summary.firstSeen)} → {fmtDateSec(journey.summary.lastSeen)}
              </span>
            )}
          </div>
        </div>
        <button
          type="button"
          className="am-jn-phead-block"
          onClick={() => onBlockUser(selected.userId, selected.userEmail)}
          title="Bloquear novos logins deste usuário"
        >
          <Icon.Block /> Bloquear
        </button>
      </header>

      {q.isError ? (
        <div className="am-banner"><Icon.Error /><span>Não deu pra carregar a jornada deste usuário.</span><button type="button" onClick={() => q.refetch()}>Tentar de novo</button></div>
      ) : q.isLoading || !journey ? (
        <JourneyTimelineSkeleton />
      ) : journey.summary.actionCount === 0 ? (
        <EmptyState icon={<Icon.Route />} title="Sem atividade nesse intervalo" hint="Este usuário não fez nenhuma ação no período. Tente ampliar o range no topo (7d / 30d)." />
      ) : (
        <>
          <JourneySummary summary={journey.summary} capped={journey.capped} />
          <JourneySessions sessions={journey.sessions} />
        </>
      )}
    </div>
  )
}

function JourneySummary({ summary, capped }) {
  const stats = [
    { value: fmtNum(summary.actionCount), label: 'ações' },
    { value: fmtNum(summary.sessionCount), label: summary.sessionCount === 1 ? 'sessão' : 'sessões' },
    { value: fmtNum(summary.warnCount + summary.errorCount), label: 'erros', danger: (summary.warnCount + summary.errorCount) > 0 },
    { value: fmtNum(summary.destructiveCount), label: 'sensíveis', warn: summary.destructiveCount > 0 },
  ]
  const topAreas = summary.areas.slice(0, 5)
  const maxArea = topAreas.length ? topAreas[0].count : 1

  return (
    <div className="am-jn-summary">
      <div className="am-jn-stats">
        {stats.map((s, i) => (
          <span key={i} className="am-jn-stat">
            <span className={`am-jn-stat-value ${s.danger ? 'am-jn-stat-value--danger' : s.warn ? 'am-jn-stat-value--warn' : ''}`}>{s.value}</span>
            <span className="am-jn-stat-label">{s.label}</span>
          </span>
        ))}
      </div>
      <div className="am-jn-areas">
        <span className="am-jn-areas-title">Áreas mais usadas</span>
        {topAreas.map((a) => (
          <div key={a.area} className="am-jn-area">
            <span className="am-jn-area-name">{a.area}</span>
            <span className="am-jn-area-track">
              <span className="am-jn-area-fill" style={{ width: `${Math.max(4, (a.count / maxArea) * 100)}%` }} />
            </span>
            <span className="am-jn-area-count">{a.count}</span>
          </div>
        ))}
      </div>
      {capped && (
        <p className="am-jn-cap"><Icon.Info /> Mostrando as 300 ações mais recentes do período — o começo pode ter sido cortado.</p>
      )}
    </div>
  )
}

function JourneySessions({ sessions }) {
  return (
    <div className="am-jn-sessions">
      {sessions.map((s, i) => {
        const prev = i > 0 ? sessions[i - 1] : null
        const gap = prev ? s.startT - prev.endT : 0
        return (
          <div key={s.id}>
            {prev && (
              <div className="am-jn-gap"><span className="am-jn-gap-line" /><span className="am-jn-gap-text">{fmtGap(gap)}</span><span className="am-jn-gap-line" /></div>
            )}
            <JourneySession session={s} index={i + 1} />
          </div>
        )
      })}
    </div>
  )
}

function JourneySession({ session, index }) {
  const [open, setOpen] = useState(true)
  const sameDay = dayLabel(session.start) === dayLabel(session.end)
  return (
    <div className="am-jn-sess">
      <button type="button" className="am-jn-sess-head" onClick={() => setOpen((v) => !v)} aria-expanded={open}>
        <span className={`am-jn-sess-chev ${open ? 'am-jn-sess-chev--open' : ''}`}><Icon.ChevRight /></span>
        <span className="am-jn-sess-num">Sessão {index}</span>
        <span className="am-jn-sess-time">
          {dayLabel(session.start)} · {fmtClockMin(session.start)}–{fmtClockMin(session.end)}{sameDay ? '' : ` (${dayLabel(session.end)})`}
        </span>
        <span className="am-jn-sess-dur">{fmtDur(session.durationMs)}</span>
        <span className="am-jn-sess-count">{session.eventCount} {session.eventCount === 1 ? 'ação' : 'ações'}</span>
        {session.errorCount > 0 && <span className="am-jn-sess-err">{session.errorCount} com erro</span>}
      </button>
      {open && (
        <div className="am-jn-events">
          {session.items.map((it, j) => <JourneyEvent key={j} item={it} />)}
        </div>
      )}
    </div>
  )
}

function JourneyEvent({ item }) {
  const [showSamples, setShowSamples] = useState(false)
  const isGroup = item.type === 'group'
  const CatIcon = CAT_ICON[item.category] || Icon.Eye
  const sev = item.severity

  return (
    <div className={`am-jn-ev am-jn-ev--${sev}`}>
      <span className="am-jn-ev-time">{isGroup ? fmtClockMin(item.start) : fmtClock(item.ts)}</span>
      <span className={`am-jn-ev-icon am-jn-ev-icon--${sev}`} title={CAT_LABEL[item.category]}><CatIcon /></span>
      <div className="am-jn-ev-body">
        <span className="am-jn-ev-action">
          {item.action}
          {isGroup && <span className="am-jn-ev-times">{item.count}×</span>}
        </span>
        <span className="am-jn-ev-tags">
          <span className="am-jn-ev-area">{item.area}</span>
          {sev === 'destructive' && <span className="am-jn-ev-tag am-jn-ev-tag--sens">sensível</span>}
          {item.fallback && <span className="am-jn-ev-tag am-jn-ev-tag--muted" title={`${item.method} ${item.route}`}>rota não mapeada</span>}
          {isGroup && <span className="am-jn-ev-span">{fmtClockMin(item.start)}–{fmtClockMin(item.end)}</span>}
        </span>
      </div>
      <div className="am-jn-ev-meta">
        {isGroup ? (
          <>
            {item.errorCount > 0 && <span className="am-jn-ev-status am-jn-ev-status--warn">{item.errorCount} erro{item.errorCount > 1 ? 's' : ''}</span>}
            <button type="button" className="am-jn-ev-expand" onClick={() => setShowSamples((v) => !v)} title="Ver as chamadas">
              {showSamples ? 'ocultar' : 'detalhar'}
            </button>
          </>
        ) : (
          <>
            {item.status >= 400 && (
              <span className={`am-jn-ev-status ${item.status >= 500 ? 'am-jn-ev-status--err' : 'am-jn-ev-status--warn'}`}>{item.status}</span>
            )}
            {item.durationMs != null && item.durationMs > 2000 && (
              <span className="am-jn-ev-slow" title="Resposta lenta"><Icon.Timer /> {fmtMs(item.durationMs)}</span>
            )}
            <code className="am-jn-ev-route" title={`${item.method} ${item.route}`}>{item.method}</code>
          </>
        )}
      </div>
      {isGroup && showSamples && (
        <ul className="am-jn-ev-samples">
          {item.samples.map((s, k) => (
            <li key={k} className={s.status >= 400 ? 'am-jn-ev-sample--err' : ''}>
              <span>{fmtClock(s.ts)}</span>
              <code>{s.method}</code>
              <span className={s.status >= 500 ? 'am-stat am-stat--err' : s.status >= 400 ? 'am-stat am-stat--warn' : 'am-stat'}>{s.status}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

// Empty state "tutorial" (DESIGN.md §4.7): silhueta de uma timeline pra ensinar
// o que a tela mostra antes de escolher um usuário.
function JourneyGhost() {
  return (
    <div className="am-jn-ghost">
      <div className="am-jn-ghost-art" aria-hidden="true">
        {[
          { w: '58%', sens: false }, { w: '42%', sens: false }, { w: '66%', sens: true },
          { w: '38%', sens: false }, { w: '52%', sens: false },
        ].map((r, i) => (
          <div key={i} className="am-jn-ghost-row">
            <span className="am-jn-ghost-time" />
            <span className={`am-jn-ghost-node ${r.sens ? 'am-jn-ghost-node--sens' : ''}`} />
            <span className="am-jn-ghost-bar" style={{ width: r.w }} />
          </div>
        ))}
      </div>
      <div className="am-jn-ghost-copy">
        <div className="am-jn-ghost-icon"><Icon.Route /></div>
        <p className="am-jn-ghost-title">Escolha um usuário para ver a jornada</p>
        <p className="am-jn-ghost-hint">Cada ação que a pessoa fez na plataforma — telas abertas, o que criou, editou, exportou ou excluiu — em ordem, com horário. Selecione alguém na lista ao lado.</p>
      </div>
    </div>
  )
}

function JourneyTimelineSkeleton() {
  return (
    <div className="am-jn-summary">
      <div className="am-jn-stats">
        {[0, 1, 2, 3].map((i) => (
          <span key={i} className="am-jn-stat">
            <div className="am-skel" style={{ width: 34, height: 22 }} />
            <div className="am-skel" style={{ width: 44, height: 9, marginTop: 6 }} />
          </span>
        ))}
      </div>
      <div className="am-jn-events" style={{ marginTop: 18 }}>
        {Array.from({ length: 7 }).map((_, i) => (
          <div key={i} className="am-jn-ev">
            <span className="am-jn-ev-time"><div className="am-skel" style={{ width: 40, height: 10 }} /></span>
            <span className="am-jn-ev-icon am-jn-ev-icon--normal" style={{ background: 'var(--c-surface-2)' }} />
            <div className="am-jn-ev-body"><div className="am-skel" style={{ width: `${45 + (i * 7) % 40}%`, height: 12 }} /></div>
            <div className="am-jn-ev-meta" />
          </div>
        ))}
      </div>
    </div>
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

  // Jornada: usuário selecionado ({ userId, userEmail }) + busca no rail.
  const [journeyUser, setJourneyUser] = useState(null)
  const [journeySearch, setJourneySearch] = useState('')

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

  // Deep-link do painel de identidades → aba Jornada, já com o usuário escolhido.
  const viewJourney = useCallback((actor) => {
    if (!actor?.userId) return
    setJourneyUser({ userId: actor.userId, userEmail: actor.userEmail || null })
    setSelectedActor(null)
    setTab('jornada')
  }, [])

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

  // Usuários autenticados vistos no período — alimenta o rail da Jornada.
  const journeyUsers = useMemo(
    () => groupedActors.filter((a) => a.userId),
    [groupedActors],
  )

  const tabs = [
    { id: 'overview',    label: 'Visão geral',   Icon: Icon.Timeline },
    { id: 'identidades', label: 'Identidades',   Icon: Icon.Shield,
      badge: actorSummary.highRisk > 0 ? actorSummary.highRisk : null, badgeDanger: true },
    { id: 'jornada',     label: 'Jornada',       Icon: Icon.Route },
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

      {/* ── TAB: Jornada ──────────────────────────────────────────────── */}
      {tab === 'jornada' && (
        <div className="am-tab-content" key="jornada">
          <JourneyTab
            users={journeyUsers}
            loadingUsers={actorsQ.isLoading}
            selected={journeyUser}
            onSelect={setJourneyUser}
            search={journeySearch}
            onSearch={setJourneySearch}
            range={range}
            onBlockUser={blockUser}
          />
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
          onViewJourney={viewJourney}
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

// ── Chart helpers ──────────────────────────────────────────────────────────

function niceTicks(max, count = 5) {
  if (max <= 0) return [0, 1]
  const raw = max / count
  const mag = Math.pow(10, Math.floor(Math.log10(raw)))
  const norm = raw / mag
  let step
  if (norm < 1.5) step = mag
  else if (norm < 3) step = 2 * mag
  else if (norm < 7) step = 5 * mag
  else step = 10 * mag
  const end = Math.ceil(max / step) * step
  const ticks = []
  for (let v = 0; v <= end + step / 2; v += step) ticks.push(Number(v.toFixed(6)))
  return ticks
}

function fmtCompact(n) {
  if (n == null) return '0'
  const abs = Math.abs(n)
  if (abs >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (abs >= 10_000)    return `${Math.round(n / 1000)}k`
  if (abs >= 1_000)     return `${(n / 1000).toFixed(1)}k`
  return String(Math.round(n))
}

function fmtMsShort(ms) {
  if (ms == null) return '—'
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

function parsePeriod(p) {
  if (!p) return { day: '', time: '', full: '', byDay: true }
  const byDay = !p.includes('T')
  if (byDay) {
    const [y, m, d] = p.split('-')
    return { day: `${d}/${m}`, time: '', full: `${d}/${m}/${y}`, byDay: true }
  }
  const [date, time] = p.split('T')
  const [, mo, d] = date.split('-')
  const hhmm = (time || '').slice(0, 5)
  return { day: `${d}/${mo}`, time: hhmm, full: `${d}/${mo} • ${hhmm}`, byDay: false }
}

function pickXTickIndices(n) {
  if (n <= 1) return [0]
  const target = n > 24 ? 7 : 6
  const step = Math.max(1, Math.round(n / target))
  const out = []
  for (let i = 0; i < n; i += step) out.push(i)
  if (out[out.length - 1] !== n - 1) out.push(n - 1)
  return out
}

// TimelineChart: chart de volume + latência com insights agregados,
// marcadores de erros/lentos no topo e hover crosshair com tooltip.
const CHART_COLORS = {
  requests: '#E81E75',
  latency:  '#6366f1',
  errors:   '#dc2626',
  slow:     '#f59e0b',
}

function TimelineChart({ timeline }) {
  const wrapRef = useRef(null)
  const [width, setWidth] = useState(900)
  const [hoverIdx, setHoverIdx] = useState(null)
  const [visible, setVisible] = useState({ requests: true, latency: true, errors: true, slow: true })

  useEffect(() => {
    if (!wrapRef.current) return
    const ro = new ResizeObserver(([e]) => setWidth(Math.max(320, Math.round(e.contentRect.width))))
    ro.observe(wrapRef.current)
    return () => ro.disconnect()
  }, [])

  const N = timeline.length
  const byDay = N > 0 && !timeline[0].period.includes('T')

  // Insights agregados sobre o período.
  const insights = useMemo(() => {
    if (N === 0) return null
    const totalReq  = timeline.reduce((s, t) => s + (t.totalRequests || 0), 0)
    const totalErr  = timeline.reduce((s, t) => s + (t.totalErrors   || 0), 0)
    const totalSlow = timeline.reduce((s, t) => s + (t.totalSlow     || 0), 0)
    let peak = timeline[0]
    for (const t of timeline) if (t.totalRequests > peak.totalRequests) peak = t
    const avgLat = totalReq > 0
      ? Math.round(timeline.reduce((s, t) => s + (t.avgDuration || 0) * (t.totalRequests || 0), 0) / totalReq)
      : 0
    const errRate = totalReq > 0 ? (totalErr / totalReq) * 100 : 0
    const mid = Math.max(1, Math.floor(N / 2))
    const firstHalf = timeline.slice(0, mid)
    const secondHalf = timeline.slice(mid)
    const avgA = firstHalf.reduce((s, t) => s + t.totalRequests, 0) / firstHalf.length
    const avgB = secondHalf.reduce((s, t) => s + t.totalRequests, 0) / Math.max(1, secondHalf.length)
    const trendPct = avgA > 0 ? ((avgB - avgA) / avgA) * 100 : 0
    return { totalReq, totalErr, totalSlow, peak, avgLat, errRate, trendPct }
  }, [timeline, N])

  // Geometria.
  const H = 290
  const M = { top: 28, right: 64, bottom: 38, left: 60 }
  const iW = Math.max(10, width - M.left - M.right)
  const iH = H - M.top - M.bottom

  const maxReqRaw = Math.max(1, ...timeline.map((t) => t.totalRequests || 0))
  const maxLatRaw = Math.max(1, ...timeline.map((t) => t.avgDuration || 0))
  const yTicksReq = niceTicks(maxReqRaw, 5)
  const yReqMax   = Math.max(yTicksReq[yTicksReq.length - 1], 1)
  const yTicksLat = niceTicks(maxLatRaw, 5)
  const yLatMax   = Math.max(yTicksLat[yTicksLat.length - 1], 1)

  const xAt = (i) => (N <= 1 ? iW / 2 : (i / (N - 1)) * iW)
  const yReq = (v) => iH - (v / yReqMax) * iH
  const yLat = (v) => iH - (v / yLatMax) * iH

  const reqLinePath = N === 0 ? '' :
    timeline.map((t, i) => `${i === 0 ? 'M' : 'L'}${xAt(i).toFixed(2)},${yReq(t.totalRequests).toFixed(2)}`).join(' ')
  const reqAreaPath = N === 0 ? '' :
    `M${xAt(0).toFixed(2)},${iH} ${timeline.map((t, i) => `L${xAt(i).toFixed(2)},${yReq(t.totalRequests).toFixed(2)}`).join(' ')} L${xAt(N - 1).toFixed(2)},${iH} Z`
  const latLinePath = N === 0 ? '' :
    timeline.map((t, i) => `${i === 0 ? 'M' : 'L'}${xAt(i).toFixed(2)},${yLat(t.avgDuration || 0).toFixed(2)}`).join(' ')

  const xTickIdx = pickXTickIndices(N)

  const onMove = (e) => {
    if (N === 0) return
    const rect = e.currentTarget.getBoundingClientRect()
    const px = e.clientX - rect.left - M.left
    if (px < -4 || px > iW + 4) { setHoverIdx(null); return }
    const idx = N <= 1 ? 0 : Math.round((px / iW) * (N - 1))
    setHoverIdx(Math.max(0, Math.min(N - 1, idx)))
  }

  const hover = hoverIdx != null ? timeline[hoverIdx] : null
  const hoverPeriod = hover ? parsePeriod(hover.period) : null

  const trendCls = insights == null ? '' :
    insights.trendPct > 5 ? 'am-tl-ins--up' :
    insights.trendPct < -5 ? 'am-tl-ins--down' : ''

  return (
    <div className="am-tl">
      {insights && (
        <div className="am-tl-insights">
          <div className="am-tl-ins">
            <span className="am-tl-ins-value">{fmtNum(insights.totalReq)}</span>
            <span className="am-tl-ins-label">total no período</span>
          </div>
          <div className="am-tl-ins">
            <span className="am-tl-ins-value">{fmtNum(insights.peak.totalRequests)}</span>
            <span className="am-tl-ins-label">
              pico · <strong>{parsePeriod(insights.peak.period).full}</strong>
            </span>
          </div>
          <div className={`am-tl-ins ${insights.totalErr > 0 ? 'am-tl-ins--danger' : ''}`}>
            <span className="am-tl-ins-value">{fmtNum(insights.totalErr)}</span>
            <span className="am-tl-ins-label">erros 5xx · {insights.errRate.toFixed(2)}%</span>
          </div>
          <div className="am-tl-ins">
            <span className="am-tl-ins-value">{fmtMs(insights.avgLat)}</span>
            <span className="am-tl-ins-label">latência ponderada</span>
          </div>
          <div className={`am-tl-ins am-tl-ins--trend ${trendCls}`}>
            <span className="am-tl-ins-value">
              {insights.trendPct >= 0 ? '▲' : '▼'} {Math.abs(insights.trendPct).toFixed(0)}%
            </span>
            <span className="am-tl-ins-label">tendência (2ª × 1ª metade)</span>
          </div>
        </div>
      )}

      <div className="am-tl-legend">
        {[
          ['requests', 'Requests',        CHART_COLORS.requests],
          ['latency',  'Latência média',  CHART_COLORS.latency],
          ['errors',   'Erros 5xx',       CHART_COLORS.errors],
          ['slow',     'Lentos >2s',      CHART_COLORS.slow],
        ].map(([k, label, color]) => (
          <button
            key={k}
            type="button"
            className={`am-tl-legend-btn ${visible[k] ? '' : 'am-tl-legend-btn--off'}`}
            onClick={() => setVisible((v) => ({ ...v, [k]: !v[k] }))}
            style={{ '--am-tl-color': color }}
            title={visible[k] ? 'Ocultar série' : 'Exibir série'}
          >
            <span className="am-tl-legend-dot" />
            {label}
          </button>
        ))}
      </div>

      <div ref={wrapRef} className="am-tl-chart-wrap">
        <svg
          className="am-tl-svg"
          width={width}
          height={H}
          onMouseMove={onMove}
          onMouseLeave={() => setHoverIdx(null)}
        >
          <defs>
            <linearGradient id="am-tl-fill" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%"  stopColor={CHART_COLORS.requests} stopOpacity="0.22" />
              <stop offset="85%" stopColor={CHART_COLORS.requests} stopOpacity="0.02" />
            </linearGradient>
          </defs>

          <g transform={`translate(${M.left},${M.top})`}>
            {/* Grid + eixo Y esquerdo (requests) */}
            {yTicksReq.map((t, i) => (
              <g key={`yr-${i}`}>
                <line
                  x1={0} x2={iW}
                  y1={yReq(t)} y2={yReq(t)}
                  stroke={i === 0 ? '#cbd5e1' : '#e2e8f0'}
                  strokeWidth={1}
                  strokeDasharray={i === 0 ? '0' : '3 4'}
                />
                <text x={-10} y={yReq(t)} textAnchor="end" dominantBaseline="middle" className="am-tl-axis">
                  {fmtCompact(t)}
                </text>
              </g>
            ))}

            {/* Eixo Y direito (latência) */}
            {visible.latency && yTicksLat.map((t, i) => (
              <text
                key={`yl-${i}`}
                x={iW + 10} y={yLat(t)}
                textAnchor="start" dominantBaseline="middle"
                className="am-tl-axis am-tl-axis--lat"
              >
                {fmtMsShort(t)}
              </text>
            ))}

            {/* Área + linha de requests */}
            {visible.requests && (
              <>
                <path d={reqAreaPath} fill="url(#am-tl-fill)" />
                <path d={reqLinePath} fill="none" stroke={CHART_COLORS.requests} strokeWidth={2}
                      strokeLinejoin="round" strokeLinecap="round" />
              </>
            )}

            {/* Linha tracejada de latência */}
            {visible.latency && (
              <path d={latLinePath} fill="none" stroke={CHART_COLORS.latency}
                    strokeWidth={1.6} strokeDasharray="5 3" strokeLinejoin="round" strokeLinecap="round" />
            )}

            {/* Marcadores no topo: erros + lentos */}
            {(visible.errors || visible.slow) && timeline.map((t, i) => {
              const hasErr = visible.errors && t.totalErrors > 0
              const hasSlo = visible.slow && t.totalSlow > 0
              if (!hasErr && !hasSlo) return null
              return (
                <g key={`mk-${i}`}>
                  {hasErr && (
                    <>
                      <line x1={xAt(i)} x2={xAt(i)} y1={2} y2={iH}
                            stroke={CHART_COLORS.errors} strokeWidth={1} strokeDasharray="1 3" opacity={0.25} />
                      <circle cx={xAt(i)} cy={6}
                              r={Math.min(7, 3 + Math.log2(t.totalErrors + 1))}
                              fill={CHART_COLORS.errors} stroke="#fff" strokeWidth={1.4} />
                    </>
                  )}
                  {hasSlo && (
                    <circle cx={xAt(i)} cy={hasErr ? 20 : 6}
                            r={Math.min(6, 3 + Math.log2(t.totalSlow + 1))}
                            fill={CHART_COLORS.slow} stroke="#fff" strokeWidth={1.2} />
                  )}
                </g>
              )
            })}

            {/* Eixo X */}
            {xTickIdx.map((i) => {
              const lbl = parsePeriod(timeline[i].period)
              return (
                <g key={`xt-${i}`}>
                  <line x1={xAt(i)} x2={xAt(i)} y1={iH} y2={iH + 4} stroke="#cbd5e1" />
                  <text x={xAt(i)} y={iH + 18} textAnchor="middle" className="am-tl-axis">
                    {byDay ? lbl.day : lbl.time}
                  </text>
                </g>
              )
            })}

            {/* Hover crosshair + pontos */}
            {hoverIdx != null && (
              <g pointerEvents="none">
                <line x1={xAt(hoverIdx)} x2={xAt(hoverIdx)} y1={0} y2={iH}
                      stroke="#06055B" strokeWidth={1} strokeOpacity={0.35} />
                {visible.requests && (
                  <circle cx={xAt(hoverIdx)} cy={yReq(timeline[hoverIdx].totalRequests)}
                          r={4.5} fill="#fff" stroke={CHART_COLORS.requests} strokeWidth={2} />
                )}
                {visible.latency && (
                  <circle cx={xAt(hoverIdx)} cy={yLat(timeline[hoverIdx].avgDuration || 0)}
                          r={3.5} fill="#fff" stroke={CHART_COLORS.latency} strokeWidth={2} />
                )}
              </g>
            )}
          </g>

          {/* Rótulo do eixo direito (latência) */}
          {visible.latency && (
            <text x={width - 8} y={14} textAnchor="end" className="am-tl-axis-title">
              latência
            </text>
          )}
          <text x={8} y={14} textAnchor="start" className="am-tl-axis-title">
            requests por {byDay ? 'dia' : 'hora'}
          </text>
        </svg>

        {hoverIdx != null && hover && hoverPeriod && (
          <div
            className="am-tl-tooltip"
            style={{
              left: Math.min(Math.max(xAt(hoverIdx) + M.left + 14, 8), Math.max(8, width - 232)),
            }}
          >
            <div className="am-tl-tt-period">{hoverPeriod.full}</div>
            <ul className="am-tl-tt-list">
              <li>
                <span className="am-tl-tt-dot" style={{ background: CHART_COLORS.requests }} />
                Requests <strong>{fmtNum(hover.totalRequests)}</strong>
              </li>
              <li>
                <span className="am-tl-tt-dot" style={{ background: CHART_COLORS.latency }} />
                Latência <strong>{fmtMs(hover.avgDuration)}</strong>
              </li>
              {hover.totalErrors > 0 && (
                <li>
                  <span className="am-tl-tt-dot" style={{ background: CHART_COLORS.errors }} />
                  Erros 5xx <strong>{fmtNum(hover.totalErrors)}</strong>
                </li>
              )}
              {hover.totalSlow > 0 && (
                <li>
                  <span className="am-tl-tt-dot" style={{ background: CHART_COLORS.slow }} />
                  Lentos &gt;2s <strong>{fmtNum(hover.totalSlow)}</strong>
                </li>
              )}
              {hover.totalErrors === 0 && hover.totalSlow === 0 && (
                <li className="am-tl-tt-ok">Sem erros nem lentos neste bucket</li>
              )}
            </ul>
          </div>
        )}
      </div>
    </div>
  )
}
