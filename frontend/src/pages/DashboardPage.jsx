import { useState, useMemo, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { useAuth } from '../contexts/AuthContext'
import { useCampaigns, useStreamHealth, useClients } from '../api/hooks'
import api from '../api/client'
import StationAvatar from '../components/StationAvatar'
import NotificationBell from '../components/NotificationBell'
import './DashboardPage.css'

// ── Shared helpers ────────────────────────────────────────────────

function formatDate(isoString) {
  if (!isoString) return '—'
  return new Date(isoString).toLocaleDateString('pt-BR', {
    timeZone: 'America/Sao_Paulo',
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
  })
}

function formatDateFull(date) {
  return date.toLocaleDateString('pt-BR', {
    timeZone: 'America/Sao_Paulo',
    weekday: 'long',
    day: 'numeric',
    month: 'long',
    year: 'numeric',
  })
}

// Friendly "atualizado há Xs" — recomputes against `dataUpdatedAt` (ms)
function relTimeFromMs(ms) {
  if (!ms) return null
  const diff = Math.max(0, Date.now() - ms)
  const s = Math.floor(diff / 1000)
  if (s < 5)   return 'agora'
  if (s < 60)  return `há ${s}s`
  if (s < 3600) return `há ${Math.floor(s / 60)}min`
  return `há ${Math.floor(s / 3600)}h`
}

// Status keys are the lifecycle values written by the API (§18.2.1 PT-BR).
const STATUS_META = {
  ativa:      { label: 'Ativas',      badge: 'Ativa',      color: '#10b981',          badgeClass: 'badge-ativa'      },
  programada: { label: 'Programadas', badge: 'Programada', color: '#6b7280',          badgeClass: 'badge-programada' },
  concluida:  { label: 'Concluídas',  badge: 'Concluída',  color: '#3b82f6',          badgeClass: 'badge-concluida'  },
  cancelada:  { label: 'Canceladas',  badge: 'Cancelada',  color: 'rgba(239, 68, 68, 0.6)', badgeClass: 'badge-cancelada' },
}

// ═══════════════════════════════════════════════════════════════════
//  CLIENT DASHBOARD
// ═══════════════════════════════════════════════════════════════════
//
//  Layout: bento grid com hero metric (total de campanhas + distribuição
//  por status) na esquerda e lista vertical "próximas a iniciar" na
//  direita; seguido por grid de campanhas ativas e lista compacta de
//  concluídas recentes. Cliente é read-only: sem CTAs de criação,
//  apenas navegação.
//
//  Classes: prefix .cdash-* (em DashboardPage.css). NÃO colidem com
//  .dh-* do admin nem com as classes legadas .dashboard-/.campaign- em
//  index.css (mantidas vivas porque podem ser usadas por outras telas).
//
//  Estados: skeleton com forma exata, empty com ghost preview real,
//  stagger 40/100/160/220ms nos blocos principais.

// Phosphor-style inline SVG icons (mantém o padrão SVG do projeto)
const CIcon = {
  calendar: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="4" width="18" height="18" rx="2" /><line x1="16" y1="2" x2="16" y2="6" /><line x1="8" y1="2" x2="8" y2="6" /><line x1="3" y1="10" x2="21" y2="10" />
    </svg>
  ),
  broadcast: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="2" /><path d="M16.24 7.76a6 6 0 0 1 0 8.49M7.76 16.24a6 6 0 0 1 0-8.49M19.07 4.93a10 10 0 0 1 0 14.14M4.93 19.07a10 10 0 0 1 0-14.14" />
    </svg>
  ),
  arrow: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round">
      <line x1="5" y1="12" x2="19" y2="12" /><polyline points="12 5 19 12 12 19" />
    </svg>
  ),
  chevron: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="9 18 15 12 9 6" />
    </svg>
  ),
  inbox: (
    <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="22 12 16 12 14 15 10 15 8 12 2 12" /><path d="M5.45 5.11L2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z" />
    </svg>
  ),
}

// Compute days until a YYYY-MM-DD date string (Brazil time). Negative if past.
function daysUntil(dateStr) {
  if (!dateStr) return null
  const target = new Date(dateStr + 'T00:00:00-03:00')
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  const ms = target - today
  return Math.round(ms / (1000 * 60 * 60 * 24))
}

function relativeDays(n) {
  if (n == null) return ''
  if (n === 0)  return 'hoje'
  if (n === 1)  return 'amanhã'
  if (n > 0)    return `em ${n} dias`
  if (n === -1) return 'ontem'
  return `há ${Math.abs(n)} dias`
}

// "abr/24" → curto pra recent rows
function monthYearShort(dateStr) {
  if (!dateStr) return '—'
  const d = new Date(dateStr + 'T00:00:00-03:00')
  return d.toLocaleDateString('pt-BR', { month: 'short', year: '2-digit', timeZone: 'America/Sao_Paulo' }).replace('.', '')
}

// ─── Skeleton (mirrors loaded layout exactly) ─────────────

function ClientSkeleton() {
  return (
    <div className="cdash-shell" aria-busy="true" aria-live="polite">
      <div className="cdash-hello">
        <div className="cdash-skel" style={{ width: 240, height: 28, marginBottom: 6 }} />
        <div className="cdash-skel" style={{ width: 320, height: 14 }} />
      </div>

      <div className="cdash-bento">
        <div className="cdash-hero-card">
          <div className="cdash-skel" style={{ width: 110, height: 11 }} />
          <div className="cdash-skel" style={{ width: 180, height: 52, marginTop: 4 }} />
          <div style={{ marginTop: 'auto' }}>
            <div className="cdash-skel" style={{ width: '100%', height: 10, borderRadius: 999, marginBottom: 12 }} />
            <div style={{ display: 'flex', gap: 16 }}>
              {[60, 70, 80, 50].map((w, i) => (
                <div key={i} className="cdash-skel" style={{ width: w, height: 12 }} />
              ))}
            </div>
          </div>
        </div>
        <div className="cdash-side-card">
          <div className="cdash-skel" style={{ width: 140, height: 14, marginBottom: 4 }} />
          <div className="cdash-skel" style={{ width: 90, height: 11, marginBottom: 14 }} />
          {[0, 1, 2].map(i => (
            <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 12, padding: 10 }}>
              <div className="cdash-skel" style={{ width: 6, height: 6, borderRadius: '50%' }} />
              <div style={{ flex: 1 }}>
                <div className="cdash-skel" style={{ width: '70%', height: 12, marginBottom: 4 }} />
                <div className="cdash-skel" style={{ width: '45%', height: 11 }} />
              </div>
              <div className="cdash-skel" style={{ width: 40, height: 12 }} />
            </div>
          ))}
        </div>
      </div>

      <div className="cdash-section">
        <div className="cdash-skel" style={{ width: 180, height: 18 }} />
        <div className="cdash-active-grid">
          {[0, 1, 2, 3].map(i => (
            <div key={i} className="cdash-active-card" style={{ animation: 'none', cursor: 'default' }}>
              <div className="cdash-skel" style={{ width: 70, height: 11 }} />
              <div className="cdash-skel" style={{ width: '80%', height: 18 }} />
              <div className="cdash-skel" style={{ width: '55%', height: 12, marginTop: 'auto' }} />
              <div className="cdash-skel" style={{ width: '40%', height: 12 }} />
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

// ─── Empty state — ghost preview real (não placeholder genérico) ─

function ClientEmpty({ clientName }) {
  return (
    <div className="cdash-shell">
      <div className="cdash-hello">
        <h1>Bem-vindo{clientName ? `, ${clientName}` : ''}</h1>
        <div className="cdash-hello-sub">
          Esta é a sua visão geral. Quando houver campanhas, elas aparecem aqui.
        </div>
      </div>

      <div className="cdash-empty">
        {/* Ghost layer: layout real renderizado com dados fake, transparente */}
        <div className="cdash-empty-ghost" aria-hidden="true">
          <div className="cdash-bento">
            <div className="cdash-hero-card">
              <div className="cdash-hero-label">Campanhas no total</div>
              <div className="cdash-hero-figure">
                <span className="cdash-hero-number">12</span>
                <span className="cdash-hero-unit">campanhas</span>
              </div>
              <div className="cdash-distrib">
                <div className="cdash-distrib-bar">
                  <div className="cdash-distrib-seg" style={{ width: '25%', background: '#10b981' }} />
                  <div className="cdash-distrib-seg" style={{ width: '15%', background: '#6b7280' }} />
                  <div className="cdash-distrib-seg" style={{ width: '60%', background: '#3b82f6' }} />
                </div>
                <div className="cdash-distrib-legend">
                  <span className="cdash-distrib-legend-item">
                    <span className="cdash-distrib-dot" style={{ background: '#10b981' }} />
                    <span className="cdash-distrib-legend-num">3</span> ativas
                  </span>
                  <span className="cdash-distrib-legend-item">
                    <span className="cdash-distrib-dot" style={{ background: '#6b7280' }} />
                    <span className="cdash-distrib-legend-num">2</span> programadas
                  </span>
                  <span className="cdash-distrib-legend-item">
                    <span className="cdash-distrib-dot" style={{ background: '#3b82f6' }} />
                    <span className="cdash-distrib-legend-num">7</span> concluídas
                  </span>
                </div>
              </div>
            </div>
            <div className="cdash-side-card">
              <div className="cdash-side-head">
                <h3 className="cdash-side-title">Próximas a iniciar</h3>
                <span className="cdash-side-sub">2 nas próximas semanas</span>
              </div>
              <div className="cdash-side-list">
                <div className="cdash-side-item">
                  <span className="cdash-side-dot" />
                  <div className="cdash-side-body">
                    <div className="cdash-side-name">Campanha exemplo</div>
                    <div className="cdash-side-meta">30 emissoras</div>
                  </div>
                  <span className="cdash-side-countdown">em 3 dias</span>
                </div>
                <div className="cdash-side-item">
                  <span className="cdash-side-dot" />
                  <div className="cdash-side-body">
                    <div className="cdash-side-name">Outra campanha</div>
                    <div className="cdash-side-meta">22 emissoras</div>
                  </div>
                  <span className="cdash-side-countdown">em 7 dias</span>
                </div>
              </div>
            </div>
          </div>
        </div>

        {/* Action overlay focado no centro */}
        <div className="cdash-empty-overlay">
          <div className="cdash-empty-action">
            <div className="cdash-empty-icon">{CIcon.inbox}</div>
            <h3 className="cdash-empty-title">Nenhuma campanha por aqui ainda</h3>
            <p className="cdash-empty-msg">
              Assim que o administrador cadastrar suas campanhas, você verá
              o resumo, próximas a iniciar e atalhos pras veiculações nesta tela.
            </p>
          </div>
        </div>
      </div>
    </div>
  )
}

function ClientDashboard() {
  const navigate = useNavigate()
  const { user, clientId, isAdmin } = useAuth()
  const { data: campaigns = [], isLoading, error, refetch } = useCampaigns()
  // /clients (lista global) é admin-only no backend — viewer cai em 403.
  // Aqui só usamos a lista pra resolver o nome+logo do cliente vinculado,
  // e o JSX já lida bem com linkedClient = null (cdash-hello-client some).
  // Sem o gate, o ClientDashboard de qualquer viewer dispara um 403 ruidoso
  // a cada montagem.
  const clientsQ = useClients({ enabled: isAdmin && !!clientId })

  const linkedClient = clientId
    ? (clientsQ.data ?? []).find(c => c.id === clientId)
    : null

  const today = useMemo(() => formatDateFull(new Date()), [])
  const firstName = (user?.name || user?.email || '').split(/\s+/)[0] || ''

  // ── Agrupamentos ────────────────────────────────────────
  const counts = useMemo(() => {
    const c = { ativa: 0, programada: 0, concluida: 0, cancelada: 0 }
    campaigns.forEach(camp => { if (c[camp.status] !== undefined) c[camp.status]++ })
    return c
  }, [campaigns])

  const total = campaigns.length

  // Próximas a iniciar: programadas com start_date no futuro, ordenadas por proximidade
  const upcoming = useMemo(() => {
    return campaigns
      .filter(c => c.status === 'programada')
      .map(c => ({ ...c, _days: daysUntil(c.start_date) }))
      .filter(c => c._days != null && c._days >= 0)
      .sort((a, b) => a._days - b._days)
      .slice(0, 4)
  }, [campaigns])

  // Ativas em destaque (cards grandes)
  const activeCampaigns = useMemo(() => {
    return campaigns
      .filter(c => c.status === 'ativa')
      .sort((a, b) => (b.start_date || '').localeCompare(a.start_date || ''))
      .slice(0, 8)
  }, [campaigns])

  // Concluídas recentes (lista compacta)
  const recentFinished = useMemo(() => {
    return campaigns
      .filter(c => c.status === 'concluida' || c.status === 'cancelada')
      .sort((a, b) => (b.end_date || '').localeCompare(a.end_date || ''))
      .slice(0, 5)
  }, [campaigns])

  // ── Estados de loading/erro/vazio ───────────────────────
  if (isLoading) return <ClientSkeleton />
  if (error && total === 0) {
    return (
      <div className="cdash-shell">
        <div className="cdash-err">
          <div className="cdash-err-icon">
            <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/>
            </svg>
          </div>
          <h3 className="cdash-err-title">Não foi possível carregar suas campanhas</h3>
          <p className="cdash-err-msg">Tente novamente em instantes.</p>
          <button className="btn btn-secondary btn-sm" onClick={() => refetch()}>Tentar novamente</button>
        </div>
      </div>
    )
  }
  if (total === 0) return <ClientEmpty clientName={linkedClient?.name} />

  // ── Distribuição (barra empilhada) ──────────────────────
  const distribSegments = [
    { key: 'ativa',      pct: (counts.ativa      / total) * 100, color: '#10b981' },
    { key: 'programada', pct: (counts.programada / total) * 100, color: '#6b7280' },
    { key: 'concluida',  pct: (counts.concluida  / total) * 100, color: '#3b82f6' },
    { key: 'cancelada',  pct: (counts.cancelada  / total) * 100, color: 'rgba(239, 68, 68, 0.55)' },
  ].filter(s => s.pct > 0)

  return (
    <div className="cdash-shell">
      {/* ── Hello / orientação ─────────────────────────── */}
      <div className="cdash-hello">
        <h1>Olá{firstName ? `, ${firstName}` : ''}</h1>
        <div className="cdash-hello-sub">
          {linkedClient && (
            <>
              <span className="cdash-hello-client">{linkedClient.name}</span>
              <span className="cdash-hello-sep" />
            </>
          )}
          <span style={{ textTransform: 'capitalize' }}>{today}</span>
        </div>
      </div>

      {/* ── Bento: hero metric + próximas ──────────────── */}
      <div className="cdash-bento">
        <div className="cdash-hero-card">
          <div className="cdash-hero-label">Campanhas no total</div>
          <div className="cdash-hero-figure">
            <span className="cdash-hero-number">{total}</span>
            <span className="cdash-hero-unit">{total === 1 ? 'campanha' : 'campanhas'}</span>
          </div>

          <div className="cdash-distrib">
            <div className="cdash-distrib-bar" role="img" aria-label="Distribuição por status">
              {distribSegments.map(s => (
                <div
                  key={s.key}
                  className="cdash-distrib-seg"
                  style={{ width: `${s.pct}%`, background: s.color }}
                  title={`${STATUS_META[s.key].label}: ${counts[s.key]}`}
                />
              ))}
            </div>
            <div className="cdash-distrib-legend">
              {['ativa', 'programada', 'concluida', 'cancelada'].map(key => (
                counts[key] > 0 && (
                  <span key={key} className="cdash-distrib-legend-item">
                    <span className="cdash-distrib-dot" style={{ background: STATUS_META[key].color }} />
                    <span className="cdash-distrib-legend-num">{counts[key]}</span>
                    {STATUS_META[key].label.toLowerCase()}
                  </span>
                )
              ))}
            </div>
          </div>
        </div>

        <div className="cdash-side-card">
          <div className="cdash-side-head">
            <h3 className="cdash-side-title">Próximas a iniciar</h3>
            <span className="cdash-side-sub">
              {upcoming.length > 0
                ? `${upcoming.length} programada${upcoming.length === 1 ? '' : 's'}`
                : 'nenhuma agendada'}
            </span>
          </div>
          {upcoming.length === 0 ? (
            <div className="cdash-side-empty">
              Nenhuma campanha programada no momento.
            </div>
          ) : (
            <div className="cdash-side-list">
              {upcoming.map(c => {
                const urgent = c._days <= 3
                return (
                  <button
                    key={c.id}
                    type="button"
                    className="cdash-side-item"
                    onClick={() => navigate(`/campaigns?status=programada`)}
                  >
                    <span
                      className="cdash-side-dot"
                      style={{ background: STATUS_META.programada.color }}
                    />
                    <div className="cdash-side-body">
                      <div className="cdash-side-name" title={c.name}>{c.name}</div>
                      <div className="cdash-side-meta">
                        {c.target_stations != null
                          ? `${c.target_stations} emissora${c.target_stations === 1 ? '' : 's'}`
                          : 'sem emissoras definidas'}
                      </div>
                    </div>
                    <span className={`cdash-side-countdown ${urgent ? 'urgent' : ''}`}>
                      {relativeDays(c._days)}
                    </span>
                  </button>
                )
              })}
            </div>
          )}
        </div>
      </div>

      {/* ── Active campaigns grid ──────────────────────── */}
      {activeCampaigns.length > 0 && (
        <div className="cdash-section delay-1">
          <div className="cdash-section-head">
            <div>
              <h2 className="cdash-section-title">
                Em atividade agora{' '}
                <span className="cdash-section-count">· {counts.ativa} ativa{counts.ativa === 1 ? '' : 's'}</span>
              </h2>
            </div>
            <button
              type="button"
              className="cdash-section-link"
              onClick={() => navigate('/campaigns?status=ativa')}
            >
              Ver todas {CIcon.arrow}
            </button>
          </div>
          <div className="cdash-active-grid">
            {activeCampaigns.map(c => (
              <button
                key={c.id}
                type="button"
                className="cdash-active-card"
                style={{ ['--cdash-tone']: STATUS_META.ativa.color }}
                onClick={() => navigate(`/detections?campaign_id=${c.id}`)}
              >
                <div className="cdash-active-status">
                  <span className="cdash-active-status-dot" />
                  ATIVA
                </div>
                <h3 className="cdash-active-name" title={c.name}>{c.name}</h3>
                <div className="cdash-active-meta">
                  <div className="cdash-active-meta-row">
                    {CIcon.calendar}
                    {formatDate(c.start_date)} – {formatDate(c.end_date)}
                  </div>
                  {c.target_stations != null && (
                    <div className="cdash-active-meta-row">
                      {CIcon.broadcast}
                      {c.target_stations} emissora{c.target_stations === 1 ? '' : 's'}
                    </div>
                  )}
                </div>
              </button>
            ))}
          </div>
        </div>
      )}

      {/* ── Recent finished list ───────────────────────── */}
      {recentFinished.length > 0 && (
        <div className="cdash-section delay-2">
          <div className="cdash-section-head">
            <div>
              <h2 className="cdash-section-title">
                Concluídas recentes{' '}
                <span className="cdash-section-count">· últimas {recentFinished.length}</span>
              </h2>
            </div>
            <button
              type="button"
              className="cdash-section-link"
              onClick={() => navigate('/campaigns?status=concluida')}
            >
              Histórico {CIcon.arrow}
            </button>
          </div>
          <div className="cdash-recent">
            {recentFinished.map(c => (
              <button
                key={c.id}
                type="button"
                className="cdash-recent-row"
                onClick={() => navigate(`/detections?campaign_id=${c.id}`)}
              >
                <span
                  className="cdash-recent-status-dot"
                  style={{ background: STATUS_META[c.status]?.color ?? 'var(--c-text-3)' }}
                  title={STATUS_META[c.status]?.label}
                />
                <span className="cdash-recent-name" title={c.name}>{c.name}</span>
                <span className="cdash-recent-period">{monthYearShort(c.end_date)}</span>
                <span className="cdash-recent-stations">
                  {c.target_stations != null ? `${c.target_stations} emissora${c.target_stations === 1 ? '' : 's'}` : '—'}
                </span>
                <span className="cdash-recent-chev">{CIcon.chevron}</span>
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

// ═══════════════════════════════════════════════════════════════════
//  ADMIN DASHBOARD — System Health
// ═══════════════════════════════════════════════════════════════════

// Inline hooks for /health and /workers — kept here (not in api/hooks.js)
// because that file is off-limits for this change.
function useSystemHealth() {
  return useQuery({
    queryKey: ['system-health'],
    queryFn: () => api.get('/health').then(r => r.data),
    refetchInterval: 15_000,
    retry: 1,
  })
}

function useWorkers() {
  return useQuery({
    queryKey: ['workers-overview'],
    queryFn: () => api.get('/workers').then(r => r.data),
    refetchInterval: 10_000,
    retry: 1,
  })
}

// Workers shape from /v1/internal/workers (supervisor.WorkerStatus):
//   { station_id, active, last_pcm_at, stall_risk }
// We derive a UI status from active + stall_risk + last_pcm_at.
function deriveWorkerStatus(w) {
  if (!w?.active) return 'down'
  const last = w.last_pcm_at ? new Date(w.last_pcm_at).getTime() : 0
  if (last && Date.now() - last > 90_000) return 'down'
  if (w.stall_risk) return 'stalled'
  return 'running'
}

const WORKER_STATUS_LABEL = {
  running:    'Operando',
  stalled:    'Lento',
  restarting: 'Reiniciando',
  down:       'Caído',
}

// Tone helpers --------------------------------------------------------
function uptimeTone(pct) {
  if (pct == null || Number.isNaN(pct)) return ''
  if (pct >= 99)  return 'tone-ok'
  if (pct >= 95)  return 'tone-warn'
  return 'tone-bad'
}

// ── Building blocks ────────────────────────────────────────────────

function KpiCard({ label, value, suffix, valueTone, icon, tone, chips, isFetching, tooltip }) {
  return (
    <div className="dh-kpi" title={tooltip || undefined}>
      {isFetching && <div className="dh-kpi-refresh-bar" />}
      <div className="dh-kpi-head">
        <div className="dh-kpi-label">{label}</div>
        <div className={`dh-kpi-icon tone-${tone || 'info'}`}>{icon}</div>
      </div>
      <div className={`dh-kpi-value ${valueTone || ''}`}>
        {value}
        {suffix && <span className="dh-kpi-value-suffix">{suffix}</span>}
      </div>
      <div className="dh-kpi-foot">
        {chips?.map((c, i) => (
          <span key={i} className={`dh-chip ${c.tone ? `tone-${c.tone}` : ''}`}>
            <span className="dh-chip-dot" />
            {c.label}
          </span>
        ))}
      </div>
    </div>
  )
}

function KpiSkeleton() {
  return (
    <div className="dh-kpi">
      <div className="dh-kpi-head">
        <div className="dh-skel" style={{ width: 90, height: 11 }} />
        <div className="dh-skel" style={{ width: 36, height: 36, borderRadius: 'var(--radius-md)' }} />
      </div>
      <div className="dh-skel" style={{ width: 110, height: 32, marginTop: 6 }} />
      <div className="dh-kpi-foot">
        <div className="dh-skel" style={{ width: 70, height: 18, borderRadius: 999 }} />
        <div className="dh-skel" style={{ width: 60, height: 18, borderRadius: 999 }} />
      </div>
    </div>
  )
}

// Inline icons (Lucide-shaped, no extra dep) -------------------------
const ICONS = {
  pulse: (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M3 12h4l2-7 4 14 2-7h6" />
    </svg>
  ),
  workers: (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="4" width="18" height="6" rx="1.5" />
      <rect x="3" y="14" width="18" height="6" rx="1.5" />
      <circle cx="7" cy="7" r="0.8" fill="currentColor" />
      <circle cx="7" cy="17" r="0.8" fill="currentColor" />
    </svg>
  ),
  radio: (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M4.9 19.1A10 10 0 0 1 4.9 4.9" />
      <path d="M7.8 16.2a6 6 0 0 1 0-8.4" />
      <circle cx="12" cy="12" r="2" />
      <path d="M16.2 16.2a6 6 0 0 0 0-8.4" />
      <path d="M19.1 19.1a10 10 0 0 0 0-14.2" />
    </svg>
  ),
  trending: (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="23 6 13.5 15.5 8.5 10.5 1 18" />
      <polyline points="17 6 23 6 23 12" />
    </svg>
  ),
  programada: (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="4" width="18" height="18" rx="2" />
      <line x1="16" y1="2" x2="16" y2="6" />
      <line x1="8"  y1="2" x2="8"  y2="6" />
      <line x1="3" y1="10" x2="21" y2="10" />
    </svg>
  ),
  ativa: (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <polygon points="5 3 19 12 5 21 5 3" />
    </svg>
  ),
  concluida: (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14" />
      <polyline points="22 4 12 14.01 9 11.01" />
    </svg>
  ),
  cancelada: (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="10" />
      <line x1="15" y1="9" x2="9"  y2="15" />
      <line x1="9"  y1="9" x2="15" y2="15" />
    </svg>
  ),
  arrow: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <line x1="5"  y1="12" x2="19" y2="12" />
      <polyline points="12 5 19 12 12 19" />
    </svg>
  ),
  check: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="20 6 9 17 4 12" />
    </svg>
  ),
  alert: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
      <line x1="12" y1="9"  x2="12" y2="13" />
      <line x1="12" y1="17" x2="12.01" y2="17" />
    </svg>
  ),
}

// ── Hero strip ─────────────────────────────────────────────────────

function HeroStrip({ health, healthFetching, workers, workersFetching, streamHealth, streamHealthFetching, healthError }) {
  const hasHealth = !!health && !healthError
  const isOk = hasHealth && health.status === 'ok'

  // Sistema --------------------------------------------------------
  const systemTone = healthError ? 'bad' : isOk ? 'ok' : 'warn'
  const systemValue = healthError ? 'Indisponível' : isOk ? 'Operacional' : 'Degradado'
  const systemChips = []
  if (hasHealth) {
    const pgOk = health.deps?.postgres === 'ok'
    const natsOk = health.deps?.nats === 'ok'
    systemChips.push({ label: `Postgres ${pgOk ? 'ok' : 'down'}`, tone: pgOk ? 'ok' : 'bad' })
    systemChips.push({ label: `NATS ${natsOk ? 'ok' : 'down'}`,    tone: natsOk ? 'ok' : 'bad' })
  } else if (healthError) {
    systemChips.push({ label: 'sem resposta', tone: 'bad' })
  }

  // Workers --------------------------------------------------------
  const workerList = workers?.workers ?? []
  const total = workerList.length
  const derived = workerList.map(deriveWorkerStatus)
  const running = derived.filter(s => s === 'running').length
  const stalled = derived.filter(s => s === 'stalled').length
  const down    = derived.filter(s => s === 'down').length
  const wTone =
    total === 0      ? 'warn' :
    down > 0         ? 'bad'  :
    stalled > 0      ? 'warn' : 'ok'
  const wValueTone =
    total === 0      ? 'tone-warn' :
    down > 0         ? 'tone-bad'  :
    stalled > 0      ? 'tone-warn' : 'tone-ok'
  const wChips = []
  if (stalled > 0) wChips.push({ label: `${stalled} lento${stalled > 1 ? 's' : ''}`, tone: 'warn' })
  if (down > 0)    wChips.push({ label: `${down} caído${down > 1 ? 's' : ''}`,       tone: 'bad'  })
  if (stalled === 0 && down === 0 && total > 0) {
    wChips.push({ label: 'todos ok', tone: 'ok' })
  }
  const clapTooltip = workers
    ? `CLAP verifier: ${workers.clap_verifier ? 'online' : 'offline'}`
    : ''

  // Streams --------------------------------------------------------
  const stations = streamHealth ?? []
  const stTotal = stations.length
  const stDown = stations.filter(s => s.is_currently_down).length
  const stUp = Math.max(0, stTotal - stDown)
  const stUpPct = stTotal > 0 ? (stUp / stTotal) * 100 : 0
  const stTone =
    stTotal === 0     ? 'warn' :
    stUpPct >= 98     ? 'ok'   :
    stUpPct >= 95     ? 'warn' : 'bad'
  const stValueTone =
    stTotal === 0     ? 'tone-warn' :
    stUpPct >= 98     ? 'tone-ok'   :
    stUpPct >= 95     ? 'tone-warn' : 'tone-bad'
  const stChips = []
  if (stDown > 0) stChips.push({ label: `${stDown} fora do ar`, tone: 'bad' })
  else if (stTotal > 0) stChips.push({ label: 'tudo no ar', tone: 'ok' })

  // Uptime médio --------------------------------------------------
  const avgUptime = stTotal > 0
    ? stations.reduce((acc, s) => acc + (s.uptime_pct ?? 0), 0) / stTotal
    : null
  const upTone =
    avgUptime == null ? 'warn' :
    avgUptime >= 99   ? 'ok'   :
    avgUptime >= 95   ? 'warn' : 'bad'
  const upValueTone =
    avgUptime == null ? '' :
    avgUptime >= 99   ? 'tone-ok'   :
    avgUptime >= 95   ? 'tone-warn' : 'tone-bad'

  return (
    <div className="dh-hero">
      <KpiCard
        label="Sistema"
        icon={ICONS.pulse}
        tone={systemTone}
        valueTone={systemTone === 'ok' ? 'tone-ok' : systemTone === 'warn' ? 'tone-warn' : 'tone-bad'}
        value={systemValue}
        chips={systemChips}
        isFetching={healthFetching}
      />
      <KpiCard
        label="Workers"
        icon={ICONS.workers}
        tone={wTone}
        valueTone={wValueTone}
        value={total > 0 ? `${running}` : '—'}
        suffix={total > 0 ? `/ ${total}` : null}
        chips={wChips}
        isFetching={workersFetching}
        tooltip={clapTooltip}
      />
      <KpiCard
        label="Streams ao ar"
        icon={ICONS.radio}
        tone={stTone}
        valueTone={stValueTone}
        value={stTotal > 0 ? `${stUp}` : '—'}
        suffix={stTotal > 0 ? `/ ${stTotal}` : null}
        chips={stChips}
        isFetching={streamHealthFetching}
      />
      <KpiCard
        label="Uptime médio 24h"
        icon={ICONS.trending}
        tone={upTone}
        valueTone={upValueTone}
        value={avgUptime == null ? '—' : avgUptime.toFixed(1)}
        suffix={avgUptime == null ? null : '%'}
        chips={
          avgUptime == null
            ? [{ label: 'sem dados', tone: 'warn' }]
            : [{ label: 'janela 24h', tone: avgUptime >= 99 ? 'ok' : avgUptime >= 95 ? 'warn' : 'bad' }]
        }
        isFetching={streamHealthFetching}
      />
    </div>
  )
}

// ── Campaigns by status row ────────────────────────────────────────

function CampaignsByStatus({ campaigns }) {
  const navigate = useNavigate()
  const counts = useMemo(() => {
    const c = { programada: 0, ativa: 0, concluida: 0, cancelada: 0 }
    campaigns.forEach(camp => { if (c[camp.status] !== undefined) c[camp.status]++ })
    return c
  }, [campaigns])

  const total = campaigns.length

  const tiles = [
    { key: 'programada', label: 'Programadas', icon: ICONS.programada },
    { key: 'ativa',      label: 'Ativas',      icon: ICONS.ativa      },
    { key: 'concluida',  label: 'Concluídas',  icon: ICONS.concluida  },
    { key: 'cancelada',  label: 'Canceladas',  icon: ICONS.cancelada  },
  ]

  return (
    <div className="dh-status-card">
      <div className="dh-status-head">
        <div className="dh-status-title">Campanhas por status</div>
        <div className="dh-status-total">{total} no total</div>
      </div>
      <div className="dh-status-grid">
        {tiles.map(t => (
          <button
            key={t.key}
            type="button"
            className="dh-status-tile"
            onClick={() => navigate(`/campaigns?status=${t.key}`)}
          >
            <div className={`dh-status-tile-icon tone-${t.key}`}>{t.icon}</div>
            <div className="dh-status-tile-body">
              <div className="dh-status-tile-count">{counts[t.key]}</div>
              <div className="dh-status-tile-label">{t.label}</div>
            </div>
          </button>
        ))}
      </div>
    </div>
  )
}

// ── Station health panel ───────────────────────────────────────────

function relativeTime(iso) {
  if (!iso) return null
  const diff = Date.now() - new Date(iso)
  const s = Math.floor(diff / 1000)
  if (s < 60)    return `há ${s}s`
  if (s < 3600)  return `há ${Math.floor(s / 60)}min`
  if (s < 86400) return `há ${Math.floor(s / 3600)}h`
  return `há ${Math.floor(s / 86400)}d`
}

function StationHealthPanel({ stations, isLoading, isFetching }) {
  const navigate = useNavigate()

  const sorted = useMemo(() => {
    return [...(stations ?? [])].sort((a, b) => {
      if (a.is_currently_down && !b.is_currently_down) return -1
      if (b.is_currently_down && !a.is_currently_down) return 1
      const au = a.uptime_pct ?? 100
      const bu = b.uptime_pct ?? 100
      if (au !== bu) return au - bu
      return (a.name ?? '').localeCompare(b.name ?? '')
    })
  }, [stations])

  const top = sorted.slice(0, 10)
  const hasMore = sorted.length > 10

  return (
    <div className="dh-panel">
      {isFetching && <div className="dh-kpi-refresh-bar" />}
      <div className="dh-panel-head">
        <div>
          <div className="dh-panel-title">Saúde por estação</div>
          <div className="dh-panel-sub">
            {isLoading ? 'carregando…' : `Piores ${top.length} de ${sorted.length} ativas`}
          </div>
        </div>
        {hasMore && (
          <button className="dh-panel-link" onClick={() => navigate('/monitoring')}>
            Ver todas {ICONS.arrow}
          </button>
        )}
      </div>

      <div className="dh-station-list">
        {isLoading ? (
          Array.from({ length: 5 }).map((_, i) => (
            <div key={i} className="dh-skel-row">
              <div className="dh-skel" style={{ width: 28, height: 28, borderRadius: 8 }} />
              <div className="dh-skel" style={{ flex: 1, height: 12 }} />
              <div className="dh-skel" style={{ width: 80, height: 6, borderRadius: 999 }} />
              <div className="dh-skel" style={{ width: 50, height: 12 }} />
            </div>
          ))
        ) : top.length === 0 ? (
          <div className="dh-empty">Nenhuma estação ativa no momento.</div>
        ) : (
          top.map(st => {
            const tone = uptimeTone(st.uptime_pct)
            const fillTone = tone === 'tone-bad' ? 'tone-bad' : tone === 'tone-warn' ? 'tone-warn' : ''
            return (
              <button
                key={st.id}
                type="button"
                className="dh-station-row"
                onClick={() => navigate(`/stations/${st.id}/edit`)}
              >
                <StationAvatar station={st} size={28} />
                <div className="dh-station-name-wrap">
                  <div className="dh-station-name">{st.name}</div>
                  <div className="dh-station-meta">
                    {st.band}{st.frequency_mhz ? ` · ${st.frequency_mhz}` : ''}
                    {st.city ? ` · ${st.city}` : ''}{st.state ? `/${st.state}` : ''}
                  </div>
                </div>
                <div className="dh-uptime-bar-wrap">
                  <div
                    className={`dh-uptime-bar-fill ${fillTone}`}
                    style={{ width: `${Math.max(0, Math.min(100, st.uptime_pct ?? 0))}%` }}
                  />
                </div>
                <div className={`dh-uptime-pct ${tone}`}>
                  {(st.uptime_pct ?? 0).toFixed(1)}%
                </div>
                <div className={`dh-station-incident ${st.is_currently_down ? 'has-incident' : ''}`}>
                  {st.is_currently_down
                    ? 'Fora do ar'
                    : st.last_incident_at
                      ? `Queda ${relativeTime(st.last_incident_at)}`
                      : 'Sem quedas'}
                </div>
                <div className="dh-station-arrow">{ICONS.arrow}</div>
              </button>
            )
          })
        )}
      </div>
    </div>
  )
}

// ── Workers in alert panel ────────────────────────────────────────

function WorkersAlertPanel({ workers, isLoading, isFetching }) {
  const navigate = useNavigate()
  const list = workers?.workers ?? []

  const alerts = useMemo(() => {
    return list
      .map(w => ({ ...w, derived: deriveWorkerStatus(w) }))
      .filter(w => w.derived !== 'running')
  }, [list])

  return (
    <div className="dh-panel">
      {isFetching && <div className="dh-kpi-refresh-bar" />}
      <div className="dh-panel-head">
        <div>
          <div className="dh-panel-title">Workers em alerta</div>
          <div className="dh-panel-sub">
            {isLoading ? 'carregando…' : `${list.length} worker${list.length === 1 ? '' : 's'} ativo${list.length === 1 ? '' : 's'}`}
          </div>
        </div>
        {alerts.length > 0 && (
          <button className="dh-panel-link" onClick={() => navigate('/operations')}>
            Operações {ICONS.arrow}
          </button>
        )}
      </div>

      {isLoading ? (
        <div className="dh-skel-row">
          <div className="dh-skel" style={{ flex: 1, height: 14 }} />
          <div className="dh-skel" style={{ width: 60, height: 18, borderRadius: 999 }} />
          <div className="dh-skel" style={{ width: 80, height: 12 }} />
        </div>
      ) : alerts.length === 0 ? (
        <div className="dh-worker-empty">
          <div className="dh-worker-empty-icon">{ICONS.check}</div>
          <div className="dh-worker-empty-text">
            <div className="dh-worker-empty-title">Tudo certo</div>
            <div className="dh-worker-empty-sub">Todos os workers operando normalmente.</div>
          </div>
        </div>
      ) : (
        <>
          <div className="dh-worker-list">
            {alerts.map(w => {
              const tone = w.derived === 'down' ? 'bad' : 'warn'
              const last = w.last_pcm_at
                ? `último PCM ${relativeTime(w.last_pcm_at)}`
                : 'sem PCM recente'
              const id = (w.station_id ?? '').slice(0, 8)
              return (
                <div key={w.station_id} className={`dh-worker-row tone-${tone}`}>
                  <div className="dh-worker-id">{id}…</div>
                  <span className={`dh-worker-status tone-${tone}`}>
                    {WORKER_STATUS_LABEL[w.derived] ?? w.derived}
                  </span>
                  <div className="dh-worker-meta">{last}</div>
                </div>
              )
            })}
          </div>
          <div className="dh-worker-link-row">
            <button className="dh-panel-link" onClick={() => navigate('/operations')}>
              Abrir painel de operações {ICONS.arrow}
            </button>
          </div>
        </>
      )}
    </div>
  )
}

// ── Admin shell ───────────────────────────────────────────────────

function AdminDashboard() {
  const health        = useSystemHealth()
  const workers       = useWorkers()
  const streamHealth  = useStreamHealth({ days: 1 })
  const campaignsQ    = useCampaigns()

  // Determine the most recent successful refresh among the four sources
  // so the header timestamp reflects "freshest data".
  const newest = Math.max(
    health.dataUpdatedAt        || 0,
    workers.dataUpdatedAt       || 0,
    streamHealth.dataUpdatedAt  || 0,
    campaignsQ.dataUpdatedAt    || 0,
  )

  // Re-render every 5s so "atualizado há Xs" stays alive between fetches.
  const [, setTick] = useState(0)
  useEffect(() => {
    const t = setInterval(() => setTick(x => x + 1), 5000)
    return () => clearInterval(t)
  }, [])

  // /health is the only endpoint that gates the "API indisponível" banner —
  // a real 5xx indicates the management API itself is unreachable, not just
  // a slow query somewhere downstream.
  const apiDown =
    !!health.error &&
    !health.data &&
    !health.isLoading

  const heroLoading = health.isLoading || workers.isLoading || streamHealth.isLoading

  return (
    <div className="dh-shell">
      <div className="dh-header">
        <div className="dh-header-titles">
          <h1 className="dh-header-title">Operação E-monitor</h1>
          <div className="dh-header-sub">Visão consolidada do sistema</div>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
          <div className="dh-header-stamp">
            atualizado {newest ? relTimeFromMs(newest) : '…'}
          </div>
          <NotificationBell />
        </div>
      </div>

      {apiDown && (
        <div className="dh-banner" role="alert">
          <span className="dh-banner-icon">{ICONS.alert}</span>
          API indisponível — tentando reconectar...
        </div>
      )}

      {heroLoading && !health.data && !workers.data && !streamHealth.data ? (
        <div className="dh-hero">
          <KpiSkeleton />
          <KpiSkeleton />
          <KpiSkeleton />
          <KpiSkeleton />
        </div>
      ) : (
        <HeroStrip
          health={health.data}
          healthFetching={health.isFetching}
          healthError={apiDown}
          workers={workers.data}
          workersFetching={workers.isFetching}
          streamHealth={streamHealth.data}
          streamHealthFetching={streamHealth.isFetching}
        />
      )}

      <CampaignsByStatus campaigns={campaignsQ.data ?? []} />

      <div className="dh-cols">
        <StationHealthPanel
          stations={streamHealth.data ?? []}
          isLoading={streamHealth.isLoading}
          isFetching={streamHealth.isFetching}
        />
        <WorkersAlertPanel
          workers={workers.data}
          isLoading={workers.isLoading}
          isFetching={workers.isFetching}
        />
      </div>
    </div>
  )
}

// ═══════════════════════════════════════════════════════════════════
//  Page entry — toggle by role
// ═══════════════════════════════════════════════════════════════════

export default function DashboardPage() {
  const { isAdmin } = useAuth()
  return isAdmin ? <AdminDashboard /> : <ClientDashboard />
}
