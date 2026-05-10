import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import api from '../api/client'
import { useConfirm } from '../components/ConfirmModal'
import './WebhookDeliveriesPage.css'

/* ── Constants ────────────────────────────────────────────────── */

const ALLOWED_EVENTS = [
  { value: 'detection.confirmed', label: 'Detecção confirmada' },
  { value: 'detection.retracted', label: 'Detecção retratada' },
  { value: 'webhook.test',        label: 'Teste de webhook' },
  { value: '*',                   label: 'Todos (curinga)' },
]

const STATUS_FILTERS = [
  { value: 'all',       label: 'Todos' },
  { value: 'pending',   label: 'Pending' },
  { value: 'delivered', label: 'Delivered' },
  { value: 'failed',    label: 'Failed' },
  { value: 'dead',      label: 'Dead' },
]

/* ── Icons ────────────────────────────────────────────────────── */

function ArrowLeftIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
      <path d="M8 3L4 7l4 4M4 7h7" />
    </svg>
  )
}

function ChevronDownIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
      <path d="M3 4.5L6 7.5l3-3" />
    </svg>
  )
}

function RefreshIcon({ spinning }) {
  return (
    <svg className={`whd-reload-icon ${spinning ? 'spinning' : ''}`} width="13" height="13" viewBox="0 0 13 13" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <path d="M11 6.5a4.5 4.5 0 1 1-1.32-3.18M11 1.5v3h-3" />
    </svg>
  )
}

function TestIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <path d="M3 1.5h8M5 1.5v3.5L2 11.5a1 1 0 0 0 .9 1.5h8.2a1 1 0 0 0 .9-1.5L9 5V1.5" />
    </svg>
  )
}

function WebhookBigIcon() {
  return (
    <svg width="32" height="32" viewBox="0 0 32 32" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="9" cy="8" r="3" />
      <circle cx="23" cy="8" r="3" />
      <circle cx="16" cy="24" r="3" />
      <path d="M11.5 10l3.5 11M20.5 10L17 21M11 8h10" />
    </svg>
  )
}

/* ── Helpers ──────────────────────────────────────────────────── */

function formatAbsolute(s) {
  if (!s) return '—'
  try { return new Date(s).toLocaleString('pt-BR') } catch { return s }
}

function formatRelative(s) {
  if (!s) return '—'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  const diff = (Date.now() - d.getTime()) / 1000
  if (diff < 0)        return 'em breve'
  if (diff < 5)        return 'agora'
  if (diff < 60)       return `há ${Math.floor(diff)}s`
  if (diff < 3600)     return `há ${Math.floor(diff / 60)}min`
  if (diff < 86400)    return `há ${Math.floor(diff / 3600)}h`
  if (diff < 86400*7)  return `há ${Math.floor(diff / 86400)}d`
  return d.toLocaleDateString('pt-BR')
}

function maskUrl(url) {
  if (!url) return ''
  try {
    const u = new URL(url)
    return `${u.protocol}//${u.host}${u.pathname}`
  } catch { return url }
}

function isLocalhostUrl(s) {
  try {
    const u = new URL(s)
    return /^(localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\])$/i.test(u.hostname)
  } catch { return false }
}

function validateUrl(s) {
  if (!s) return 'Informe a URL do endpoint.'
  let u
  try { u = new URL(s) } catch { return 'URL inválida.' }
  if (u.protocol === 'https:') return null
  if (u.protocol === 'http:' && isLocalhostUrl(s)) return null
  return 'URL deve usar https:// (http:// só é permitido para localhost).'
}

function eventChipClass(evt) {
  if (evt === 'detection.confirmed') return 'whd-evt-chip--confirmed'
  if (evt === 'detection.retracted') return 'whd-evt-chip--retracted'
  if (evt === 'webhook.test')        return 'whd-evt-chip--test'
  return 'whd-evt-chip--other'
}

function statusPillClass(status) {
  return `whd-status-pill whd-status-pill--${status}`
}

function quantile(values, q) {
  if (!values.length) return null
  const sorted = [...values].sort((a, b) => a - b)
  const pos = (sorted.length - 1) * q
  const base = Math.floor(pos)
  const rest = pos - base
  if (sorted[base + 1] != null) return sorted[base] + rest * (sorted[base + 1] - sorted[base])
  return sorted[base]
}

function tryFormatJson(v) {
  if (v == null) return ''
  if (typeof v === 'string') {
    try { return JSON.stringify(JSON.parse(v), null, 2) } catch { return v }
  }
  try { return JSON.stringify(v, null, 2) } catch { return String(v) }
}

/* ── Inline hooks (kept inline per task spec) ─────────────────── */

function useClientById(clientId) {
  return useQuery({
    queryKey: ['clients-list-for-webhook'],
    queryFn: () => api.get('/clients').then(r => r.data.data ?? r.data ?? []),
    select: (list) => list.find(c => String(c.id) === String(clientId)) ?? null,
    enabled: !!clientId,
  })
}

function useWebhookConfig(clientId) {
  return useQuery({
    queryKey: ['webhook-config', clientId],
    queryFn: () => api.get(`/clients/${clientId}/webhook`).then(r => r.data),
    enabled: !!clientId,
  })
}

function useWebhookDeliveries(clientId, params) {
  return useQuery({
    queryKey: ['webhook-deliveries', clientId, params],
    queryFn: () =>
      api.get(`/clients/${clientId}/webhook-deliveries`, { params })
        .then(r => r.data.data ?? r.data ?? []),
    enabled: !!clientId,
    refetchInterval: 10_000,
  })
}

function useUpdateConfig(clientId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body) => api.patch(`/clients/${clientId}/webhook`, body).then(r => r.data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['webhook-config', clientId] })
      qc.invalidateQueries({ queryKey: ['webhook-deliveries', clientId] })
    },
  })
}

function useTest(clientId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.post(`/clients/${clientId}/webhook-test`).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['webhook-deliveries', clientId] }),
  })
}

/* ── Toast ────────────────────────────────────────────────────── */

function Toast({ kind, message }) {
  return (
    <div className={`whd-toast whd-toast--${kind}`} role="status">
      {message}
    </div>
  )
}

/* ── Skeletons ────────────────────────────────────────────────── */

function HeaderSkeleton() {
  return (
    <div className="whd-header-skel">
      <div className="whd-skeleton" style={{ width: 280, height: 24 }} />
      <div className="whd-skeleton" style={{ width: 360, height: 12 }} />
    </div>
  )
}

function TableSkeleton() {
  return (
    <div className="whd-table-wrap">
      {Array.from({ length: 5 }).map((_, i) => (
        <div key={i} className="whd-skel-row">
          <div className="whd-skeleton" />
          <div className="whd-skeleton" />
          <div className="whd-skeleton" style={{ width: 70 }} />
          <div className="whd-skeleton" />
          <div className="whd-skeleton" />
          <div className="whd-skeleton" />
          <div className="whd-skeleton" style={{ width: 26, height: 26, borderRadius: 6 }} />
        </div>
      ))}
    </div>
  )
}

/* ── Empty state ──────────────────────────────────────────────── */

function EmptyState({ onConfigure, hasUrl }) {
  return (
    <div className="whd-empty">
      <div className="whd-empty-action">
        <div className="whd-empty-icon">
          <WebhookBigIcon />
        </div>
        <h3>{hasUrl ? 'Nenhuma entrega ainda' : 'Webhook não configurado'}</h3>
        <p>
          Quando o sistema confirma uma detecção, ele envia um <code>POST</code> para a URL
          configurada com o evento <code>detection.confirmed</code> e uma assinatura{' '}
          <code>HMAC-SHA256</code> no header <code>X-Radiocheck-Signature</code>. Falhas são
          reentregues com backoff exponencial e retidas em DLQ após esgotar tentativas.
        </p>
        <button className="btn btn-primary" onClick={onConfigure}>
          {hasUrl ? 'Disparar webhook de teste' : 'Configurar agora'}
        </button>
      </div>

      <div className="whd-empty-preview" aria-hidden="true">
        {[
          { evt: 'detection.confirmed', status: 'delivered' },
          { evt: 'detection.confirmed', status: 'delivered' },
          { evt: 'webhook.test',        status: 'pending'   },
          { evt: 'detection.retracted', status: 'failed'    },
        ].map((row, i) => (
          <div key={i} className="whd-empty-preview-row">
            <span className={`whd-evt-chip ${eventChipClass(row.evt)}`}>{row.evt}</span>
            <span className={statusPillClass(row.status)}>
              <span className="whd-dot" /> {row.status}
            </span>
            <span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--c-text-3)' }}>
              há {i + 1}min
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}

/* ── Main page ────────────────────────────────────────────────── */

export default function WebhookDeliveriesPage() {
  const { id: clientId } = useParams()
  const confirm = useConfirm()

  const { data: client, isLoading: clientLoading } = useClientById(clientId)
  const { data: cfg, isLoading: cfgLoading } = useWebhookConfig(clientId)
  const {
    data: deliveries = [],
    isFetching: delivFetching,
    refetch: refetchDeliveries,
  } = useWebhookDeliveries(clientId, { limit: 200 })

  const updateMutation = useUpdateConfig(clientId)
  const testMutation = useTest(clientId)

  /* form state */
  const [url, setUrl]                 = useState('')
  const [secret, setSecret]           = useState('')
  const [secretTouched, setSecretT]   = useState(false)
  const [enabled, setEnabled]         = useState(false)
  const [events, setEvents]           = useState(['detection.confirmed'])
  const [initial, setInitial]         = useState(null) // snapshot for dirty-detect
  const [validationErrors, setVE]     = useState({})

  /* filters */
  const [statusFilter, setStatusFilter] = useState('all')
  const [eventFilter, setEventFilter]   = useState('all')

  /* expanded row */
  const [expandedId, setExpandedId] = useState(null)

  /* refresh spin feedback */
  const [reloadSpin, setReloadSpin] = useState(false)
  const reloadTimer = useRef(null)

  /* toast */
  const [toast, setToast] = useState(null)
  const toastTimer = useRef(null)
  function showToast(kind, message) {
    if (toastTimer.current) clearTimeout(toastTimer.current)
    setToast({ kind, message })
    toastTimer.current = setTimeout(() => setToast(null), 3200)
  }

  useEffect(() => () => {
    if (toastTimer.current) clearTimeout(toastTimer.current)
    if (reloadTimer.current) clearTimeout(reloadTimer.current)
  }, [])

  /* hydrate form from cfg */
  useEffect(() => {
    if (!cfg) return
    const initEvents = cfg.webhook_events?.length ? cfg.webhook_events : ['detection.confirmed']
    setUrl(cfg.webhook_url ?? '')
    setEnabled(!!cfg.webhook_enabled)
    setEvents(initEvents)
    setSecret('')
    setSecretT(false)
    setInitial({
      url: cfg.webhook_url ?? '',
      enabled: !!cfg.webhook_enabled,
      events: initEvents.slice().sort().join('|'),
    })
  }, [cfg])

  const isDirty = useMemo(() => {
    if (!initial) return false
    if (url !== initial.url) return true
    if (enabled !== initial.enabled) return true
    if (events.slice().sort().join('|') !== initial.events) return true
    if (secretTouched && secret) return true
    return false
  }, [url, enabled, events, secret, secretTouched, initial])

  function toggleEvent(v) {
    setEvents(prev => prev.includes(v) ? prev.filter(e => e !== v) : [...prev, v])
  }

  function validate() {
    const errs = {}
    const urlErr = validateUrl(url.trim())
    if (urlErr) errs.url = urlErr
    if (secretTouched && secret && secret.length < 16) {
      errs.secret = 'Secret deve ter ao menos 16 caracteres.'
    }
    if (!events.length) errs.events = 'Selecione ao menos um evento.'
    setVE(errs)
    return Object.keys(errs).length === 0
  }

  async function handleSave(e) {
    e.preventDefault()
    if (!validate()) return
    const body = {
      webhook_url: url.trim() || null,
      webhook_enabled: enabled,
      webhook_events: events,
    }
    if (secretTouched && secret) body.webhook_secret = secret
    try {
      await updateMutation.mutateAsync(body)
      setSecret('')
      setSecretT(false)
      showToast('success', 'Configuração salva.')
    } catch (err) {
      const msg = err?.response?.data?.message
                ?? err?.response?.data
                ?? err?.message
                ?? 'Erro ao salvar.'
      showToast('error', String(msg).slice(0, 160))
    }
  }

  async function handleTest() {
    if (!cfg?.webhook_url) {
      showToast('error', 'Configure a URL primeiro.')
      return
    }
    try {
      await testMutation.mutateAsync()
      showToast('success', 'Evento de teste enfileirado')
    } catch (err) {
      const msg = err?.response?.data?.message ?? err?.message ?? 'Erro ao enfileirar teste.'
      showToast('error', String(msg).slice(0, 160))
    }
  }

  function handleReload() {
    setReloadSpin(true)
    refetchDeliveries()
    if (reloadTimer.current) clearTimeout(reloadTimer.current)
    reloadTimer.current = setTimeout(() => setReloadSpin(false), 700)
  }

  function focusUrlInput() {
    document.getElementById('whd-url-input')?.focus()
  }

  /* counts and filters */
  const counts = useMemo(() => {
    const c = { all: deliveries.length, pending: 0, delivered: 0, failed: 0, dead: 0 }
    for (const d of deliveries) if (c[d.status] != null) c[d.status]++
    return c
  }, [deliveries])

  const filtered = useMemo(() => {
    return deliveries.filter(d => {
      if (statusFilter !== 'all' && d.status !== statusFilter) return false
      if (eventFilter !== 'all' && d.event_type !== eventFilter) return false
      return true
    })
  }, [deliveries, statusFilter, eventFilter])

  /* KPIs */
  const kpis = useMemo(() => {
    const total = deliveries.length
    const delivered = deliveries.filter(d => d.status === 'delivered').length
    const failedDead = deliveries.filter(d => d.status === 'failed' || d.status === 'dead').length
    const deliveredAttempts = deliveries
      .filter(d => d.status === 'delivered')
      .map(d => d.attempt_count)
    const p95 = quantile(deliveredAttempts, 0.95)
    return {
      total,
      delivered,
      deliveredPct: total ? Math.round((delivered / total) * 100) : 0,
      failedDead,
      p95: p95 == null ? '—' : (Math.round(p95 * 10) / 10).toString(),
    }
  }, [deliveries])

  const lastDelivered = useMemo(() => {
    const ts = deliveries
      .filter(d => d.status === 'delivered' && d.delivered_at)
      .map(d => new Date(d.delivered_at).getTime())
      .sort((a, b) => b - a)[0]
    return ts ? new Date(ts).toISOString() : null
  }, [deliveries])

  /* derived */
  const isConfigured  = !!cfg?.webhook_url
  const isEnabled     = !!cfg?.webhook_enabled
  const showEmptyHero = !cfgLoading && !isConfigured

  /* render */
  return (
    <div className="whd-page">
      <Link to="/clients" className="whd-back">
        <ArrowLeftIcon /> Voltar para Clientes
      </Link>

      <div className="whd-header">
        {clientLoading ? (
          <HeaderSkeleton />
        ) : (
          <>
            <div className="whd-title">
              <h1>
                <small>Webhooks ·</small>
                {client?.name ?? `Cliente #${clientId}`}
              </h1>
            </div>
            <div className="whd-subtitle">
              {cfgLoading ? (
                <span className="whd-skeleton" style={{ width: 240, height: 12 }} />
              ) : isConfigured ? (
                <>
                  <code>{maskUrl(cfg.webhook_url)}</code>
                  <span className={`whd-status-chip ${isEnabled ? 'whd-status-chip--on' : 'whd-status-chip--off'}`}>
                    <span className="whd-pulse-dot" />
                    {isEnabled ? 'Ativo' : 'Inativo'}
                  </span>
                </>
              ) : (
                <span style={{ color: 'var(--c-text-3)' }}>
                  Webhook ainda não configurado para este cliente.
                </span>
              )}
            </div>
          </>
        )}
      </div>

      {/* Card 1 — Configuração */}
      <section className="whd-card">
        <div className="whd-card-title">
          <h3>Configuração</h3>
        </div>

        <div className="whd-config-grid">
          <form className="whd-config-form" onSubmit={handleSave} noValidate>
            <div className="field">
              <label htmlFor="whd-url-input">URL do endpoint</label>
              <input
                id="whd-url-input"
                className="input"
                type="url"
                value={url}
                onChange={e => setUrl(e.target.value)}
                placeholder="https://exemplo.com/webhooks/radiocheck"
                autoComplete="off"
                spellCheck={false}
              />
              {validationErrors.url && (
                <div className="whd-validation-error">{validationErrors.url}</div>
              )}
            </div>

            <div className="field">
              <label htmlFor="whd-secret-input">
                Secret HMAC
              </label>
              <input
                id="whd-secret-input"
                className="input"
                type="password"
                value={secret}
                onChange={e => { setSecret(e.target.value); setSecretT(true) }}
                placeholder={cfg?.has_secret ? 'Deixe em branco para manter atual' : 'Mínimo 16 caracteres'}
                autoComplete="new-password"
                spellCheck={false}
              />
              {cfg?.has_secret && (
                <div className="whd-secret-hint">atual: {cfg.webhook_secret_masked}</div>
              )}
              {validationErrors.secret && (
                <div className="whd-validation-error">{validationErrors.secret}</div>
              )}
            </div>

            <label className="whd-toggle-row">
              <input
                type="checkbox"
                checked={enabled}
                onChange={e => setEnabled(e.target.checked)}
              />
              <span className="whd-toggle-row-text">Ativar entrega de webhooks</span>
              <span className="whd-toggle-row-sub">
                {enabled ? 'Eventos serão entregues' : 'Eventos não serão entregues'}
              </span>
            </label>

            <div className="field">
              <label>Eventos</label>
              <div className="whd-events-grid">
                {ALLOWED_EVENTS.map(opt => {
                  const active = events.includes(opt.value)
                  return (
                    <label
                      key={opt.value}
                      className={`whd-event-check ${active ? 'checked' : ''}`}
                    >
                      <input
                        type="checkbox"
                        checked={active}
                        onChange={() => toggleEvent(opt.value)}
                      />
                      <code>{opt.value}</code>
                    </label>
                  )
                })}
              </div>
              {validationErrors.events && (
                <div className="whd-validation-error">{validationErrors.events}</div>
              )}
            </div>

            <div className="whd-form-actions">
              <button
                type="submit"
                className="btn btn-primary"
                disabled={updateMutation.isPending || !isDirty}
              >
                {updateMutation.isPending ? 'Salvando…' : 'Salvar'}
              </button>
              <button
                type="button"
                className="btn btn-secondary"
                onClick={handleTest}
                disabled={!isConfigured || testMutation.isPending}
                title={isConfigured ? 'Enfileira evento webhook.test' : 'Configure URL primeiro'}
              >
                <TestIcon />
                {testMutation.isPending ? 'Enviando…' : 'Testar webhook'}
              </button>
            </div>
          </form>

          <aside className="whd-side">
            <span className={`whd-side-chip ${isEnabled ? 'whd-side-chip--on' : 'whd-side-chip--off'}`}>
              <span className="whd-side-chip-dot" />
              {isEnabled ? 'Ativo' : 'Inativo'}
            </span>

            <div className="whd-side-stat">
              <span className="whd-side-stat-label">Última entrega bem-sucedida</span>
              <span className="whd-side-stat-value">
                {lastDelivered ? formatRelative(lastDelivered) : '—'}
              </span>
            </div>

            <div className="whd-side-stat">
              <span className="whd-side-stat-label">Eventos selecionados</span>
              <span className="whd-side-stat-value">
                {events.length} de {ALLOWED_EVENTS.length}
              </span>
            </div>

            <div className="whd-side-stat">
              <span className="whd-side-stat-label">Secret</span>
              <span className="whd-side-stat-value" style={{ fontSize: 13 }}>
                {cfg?.has_secret ? cfg.webhook_secret_masked || 'configurado' : 'não configurado'}
              </span>
            </div>
          </aside>
        </div>
      </section>

      {/* Card 2 — KPIs */}
      <section className="whd-kpis">
        <div className="whd-kpi">
          <span className="whd-kpi-label">Total entregas</span>
          <span className="whd-kpi-value">{kpis.total}</span>
          <span className="whd-kpi-sub">janela atual (até 200)</span>
        </div>
        <div className="whd-kpi">
          <span className="whd-kpi-label">Delivered</span>
          <span className="whd-kpi-value whd-kpi-value--success">
            {kpis.delivered}
          </span>
          <span className="whd-kpi-sub">{kpis.deliveredPct}% do total</span>
        </div>
        <div className="whd-kpi">
          <span className="whd-kpi-label">Failed / Dead</span>
          <span className="whd-kpi-value whd-kpi-value--danger">
            {kpis.failedDead}
          </span>
          <span className="whd-kpi-sub">retentativas + DLQ</span>
        </div>
        <div className="whd-kpi">
          <span className="whd-kpi-label">p95 tentativas</span>
          <span className="whd-kpi-value">{kpis.p95}</span>
          <span className="whd-kpi-sub">até sucesso</span>
        </div>
      </section>

      {/* Card 3 — Tabela */}
      <section className="whd-card">
        <div className="whd-card-title">
          <h3>Entregas recentes</h3>
          <button
            className="whd-reload"
            onClick={handleReload}
            disabled={delivFetching}
            title="Recarregar agora"
          >
            <RefreshIcon spinning={reloadSpin || delivFetching} />
            Recarregar
          </button>
        </div>

        {showEmptyHero ? (
          <EmptyState onConfigure={focusUrlInput} hasUrl={false} />
        ) : (
          <>
            <div className="whd-filters">
              <div className="whd-chips" role="tablist" aria-label="Filtro por status">
                {STATUS_FILTERS.map(s => (
                  <button
                    key={s.value}
                    role="tab"
                    aria-selected={statusFilter === s.value}
                    className={`whd-chip ${statusFilter === s.value ? 'active' : ''}`}
                    onClick={() => setStatusFilter(s.value)}
                  >
                    {s.label}
                    <span className="whd-chip-count">{counts[s.value] ?? 0}</span>
                  </button>
                ))}
              </div>

              <span className="whd-filter-divider" />

              <span className="whd-filter-label">Evento</span>
              <select
                className="whd-select"
                value={eventFilter}
                onChange={e => setEventFilter(e.target.value)}
              >
                <option value="all">Todos os eventos</option>
                {ALLOWED_EVENTS.map(e => (
                  <option key={e.value} value={e.value}>{e.value}</option>
                ))}
              </select>
            </div>

            {delivFetching && deliveries.length === 0 ? (
              <TableSkeleton />
            ) : deliveries.length === 0 ? (
              <EmptyState onConfigure={handleTest} hasUrl />
            ) : (
              <div className="whd-table-wrap">
                <table className="whd-table">
                  <thead>
                    <tr>
                      <th>Quando</th>
                      <th>Evento</th>
                      <th>Status</th>
                      <th>HTTP</th>
                      <th>Tentativas</th>
                      <th>Último erro</th>
                      <th aria-label="Expandir" style={{ width: 44 }} />
                    </tr>
                  </thead>
                  <tbody>
                    {filtered.length === 0 ? (
                      <tr>
                        <td colSpan={7} className="whd-table-empty-cell">
                          Nenhuma entrega corresponde aos filtros.
                        </td>
                      </tr>
                    ) : (
                      filtered.map(d => {
                        const open = expandedId === d.id
                        const ts = d.delivered_at || d.updated_at || d.created_at
                        const httpClass =
                          d.response_code == null ? 'whd-http--none'
                          : d.response_code >= 200 && d.response_code < 300 ? 'whd-http--ok'
                          : 'whd-http--err'
                        return (
                          <DeliveryRow
                            key={d.id}
                            delivery={d}
                            open={open}
                            onToggle={() => setExpandedId(open ? null : d.id)}
                            ts={ts}
                            httpClass={httpClass}
                          />
                        )
                      })
                    )}
                  </tbody>
                </table>
              </div>
            )}
          </>
        )}
      </section>

      {toast && <Toast kind={toast.kind} message={toast.message} />}
    </div>
  )
}

/* ── Row component ────────────────────────────────────────────── */

function DeliveryRow({ delivery: d, open, onToggle, ts, httpClass }) {
  const errorTrunc = d.last_error
    ? (d.last_error.length > 80 ? d.last_error.slice(0, 80) + '…' : d.last_error)
    : null

  return (
    <>
      <tr className="whd-row-data" onClick={onToggle}>
        <td>
          <span className="whd-when" title={formatAbsolute(ts)}>
            {formatRelative(ts)}
          </span>
        </td>
        <td>
          <span className={`whd-evt-chip ${eventChipClass(d.event_type)}`}>
            {d.event_type}
          </span>
        </td>
        <td>
          <span className={statusPillClass(d.status)}>
            <span className="whd-dot" />
            {d.status}
          </span>
        </td>
        <td>
          <span className={`whd-http ${httpClass}`}>
            {d.response_code ?? '—'}
          </span>
        </td>
        <td className="whd-attempts">{d.attempt_count}</td>
        <td>
          {errorTrunc ? (
            <div className="whd-error-cell" title={d.last_error}>{errorTrunc}</div>
          ) : (
            <div className="whd-error-cell whd-error-cell--empty">—</div>
          )}
        </td>
        <td>
          <button
            className={`whd-expand-btn ${open ? 'open' : ''}`}
            aria-label={open ? 'Recolher' : 'Expandir'}
            aria-expanded={open}
            onClick={(e) => { e.stopPropagation(); onToggle() }}
          >
            <ChevronDownIcon />
          </button>
        </td>
      </tr>
      {open && (
        <tr className="whd-row-detail">
          <td colSpan={7}>
            <div className="whd-detail-inner">
              <div className="whd-detail-block">
                <span className="whd-detail-label">
                  Payload
                  <CopyButton text={tryFormatJson(d.payload)} />
                </span>
                {d.payload ? (
                  <pre className="whd-detail-pre">{tryFormatJson(d.payload)}</pre>
                ) : (
                  <pre className="whd-detail-pre whd-detail-pre--empty">— sem payload —</pre>
                )}
              </div>

              <div className="whd-detail-block">
                <span className="whd-detail-label">
                  Resposta do servidor
                  {d.response_body && <CopyButton text={d.response_body} />}
                </span>
                {d.response_body ? (
                  <pre className="whd-detail-pre">{d.response_body}</pre>
                ) : (
                  <pre className="whd-detail-pre whd-detail-pre--empty">— sem corpo de resposta —</pre>
                )}
                <div style={{ display: 'flex', gap: 14, marginTop: 6, fontSize: 11, color: 'var(--c-text-3)' }}>
                  <span>Criado em: {formatAbsolute(d.created_at)}</span>
                  <span>Próxima tentativa: {formatAbsolute(d.next_attempt_at)}</span>
                </div>
              </div>
            </div>
          </td>
        </tr>
      )}
    </>
  )
}

function CopyButton({ text }) {
  const [copied, setCopied] = useState(false)
  function onCopy() {
    if (!text) return
    try {
      navigator.clipboard?.writeText(text)
      setCopied(true)
      setTimeout(() => setCopied(false), 1200)
    } catch { /* ignore */ }
  }
  return (
    <button type="button" className="whd-copy-btn" onClick={onCopy}>
      {copied ? 'Copiado' : 'Copiar'}
    </button>
  )
}
