import { useEffect, useMemo, useRef, useState } from 'react'
import { useParams, useNavigate, Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import api from '../api/client'
import StationAvatar from '../components/StationAvatar'
import { useIgnoreDetection, useRestoreDetection, useUploadDetectionEvidence } from '../api/hooks'
import { useAuth } from '../contexts/AuthContext'
import { useConfirm } from '../components/ConfirmModal'
import './DetectionDetailPage.css'

// ── Helpers ──────────────────────────────────────────────────────

const TZ = 'America/Sao_Paulo'

const LONG_DATETIME_FMT = new Intl.DateTimeFormat('pt-BR', {
  day: 'numeric', month: 'long', year: 'numeric',
  hour: '2-digit', minute: '2-digit', second: '2-digit',
  timeZone: TZ,
})

const SHORT_DATETIME_FMT = new Intl.DateTimeFormat('pt-BR', {
  day: '2-digit', month: '2-digit', year: 'numeric',
  hour: '2-digit', minute: '2-digit',
  timeZone: TZ,
})

function formatLongDateTime(iso) {
  if (!iso) return ''
  // Intl returns "9 de maio de 2026 14:32:18"; normalize the separator
  return LONG_DATETIME_FMT.format(new Date(iso)).replace(/(\d{4}) /, '$1, ')
}

function formatShortDateTime(iso) {
  if (!iso) return ''
  return SHORT_DATETIME_FMT.format(new Date(iso))
}

function formatBytes(bytes) {
  if (bytes == null) return '—'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`
}

// 12450 ms → "00:12.450"
function formatOffsetMs(ms) {
  if (ms == null) return '—'
  const totalMs = Math.max(0, Math.round(ms))
  const minutes = Math.floor(totalMs / 60000)
  const seconds = Math.floor((totalMs % 60000) / 1000)
  const millis  = totalMs % 1000
  return `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}.${String(millis).padStart(3, '0')}`
}

// Map rate_id (int16 stored in detections.rate_used) to playback speed.
// Per §9.7 only rate_id=0 (canonical 1.0×) is generated today; reserved IDs
// for future broadcast-variant fingerprints are mapped here so unknown values
// fall back to "rate N" instead of "0.00×".
const RATE_MULTIPLIER = { 0: 1.00 }
function formatRate(rateId) {
  if (rateId == null) return '1.00×'
  const m = RATE_MULTIPLIER[rateId]
  if (m != null) return `${m.toFixed(2)}×`
  return `rate ${rateId}`
}

function confidenceTone(confidence) {
  // confidence is 0..1
  if (confidence == null) return 'is-action'
  if (confidence >= 0.8) return 'is-action'
  if (confidence >= 0.6) return 'is-warning'
  return 'is-danger'
}

function evidenceChip(status) {
  const map = {
    available:  { label: 'Disponível',   tone: 'is-success' },
    pending:    { label: 'Pendente',     tone: 'is-warning' },
    generating: { label: 'Gerando',      tone: 'is-warning' },
    missing:    { label: 'Indisponível', tone: 'is-muted' },
    failed:     { label: 'Falhou',       tone: 'is-danger' },
  }
  return map[status] ?? { label: status ?? '—', tone: 'is-muted' }
}

// ── Icons (inline SVG; matches StationEditPage idiom) ────────────

function BackIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M10 12l-4-4 4-4" />
    </svg>
  )
}

function DownloadIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M8 2v8M5 7l3 3 3-3" />
      <path d="M2 12h12" />
    </svg>
  )
}

function ChevronRightIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M6 4l4 4-4 4" />
    </svg>
  )
}

function WarnIcon() {
  return (
    <svg width="20" height="20" viewBox="0 0 22 22" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M11 2L2 19h18L11 2z" />
      <path d="M11 9v4" />
      <circle cx="11" cy="16.5" r="0.6" fill="currentColor" />
    </svg>
  )
}

function SearchIcon({ size = 36 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="11" cy="11" r="7" />
      <path d="m20 20-3.5-3.5" />
    </svg>
  )
}

function MegaphoneIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M3 11v2a1 1 0 0 0 1 1h3l8 5V5L7 10H4a1 1 0 0 0-1 1z" />
      <path d="M19 8a4 4 0 0 1 0 8" />
    </svg>
  )
}

function CalendarIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect x="3" y="5" width="18" height="16" rx="2" />
      <path d="M16 3v4M8 3v4M3 10h18" />
    </svg>
  )
}

// ── Skeleton ─────────────────────────────────────────────────────

function Sk({ w, h = 14, r = 'var(--radius-sm)', style }) {
  return (
    <div
      className="skeleton dd-sk"
      style={{ width: w ?? '100%', height: h, borderRadius: r, flexShrink: 0, ...style }}
    />
  )
}

function PageSkeleton() {
  return (
    <div className="dd-root">
      <div className="dd-header">
        <Sk w={34} h={34} r="var(--radius-md)" />
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <Sk w={90}  h={11} />
          <Sk w={280} h={22} />
          <Sk w={210} h={12} />
        </div>
      </div>

      <div className="dd-grid">
        <div className="dd-panel">
          <div className="dd-panel-head"><Sk w={90} h={12} /></div>
          <Sk h={64} r="var(--radius-lg)" />
          <Sk w={160} h={34} r="var(--radius-md)" />
        </div>

        <div className="dd-panel">
          <div className="dd-panel-head"><Sk w={120} h={12} /></div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
            <div>
              <Sk w={90} h={11} style={{ marginBottom: 8 }} />
              <Sk h={8} r="var(--radius-full)" />
            </div>
            <div>
              <Sk w={130} h={11} style={{ marginBottom: 8 }} />
              <Sk h={8} r="var(--radius-full)" />
            </div>
            <div className="dd-metric-grid">
              <Sk h={60} r="var(--radius-md)" />
              <Sk h={60} r="var(--radius-md)" />
            </div>
            <Sk h={60} r="var(--radius-md)" />
          </div>
        </div>
      </div>

      <div className="dd-context">
        <Sk h={120} r="var(--radius-xl)" />
        <Sk h={120} r="var(--radius-xl)" />
        <Sk h={120} r="var(--radius-xl)" />
      </div>
    </div>
  )
}

// ── Sub-components ───────────────────────────────────────────────

function MetricBar({ label, value, tone = 'is-action', display }) {
  const pct = Math.max(0, Math.min(100, Math.round((value ?? 0) * 100)))
  return (
    <div className="dd-metric">
      <div className="dd-metric-row">
        <span className="dd-metric-label">{label}</span>
        <span className="dd-metric-value-sm">{display ?? `${pct.toFixed(1)}%`}</span>
      </div>
      <div className="dd-bar" role="progressbar" aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100}>
        <div className={`dd-bar-fill ${tone}`} style={{ width: `${pct}%` }} />
      </div>
    </div>
  )
}

function EvidencePanel({ detection, evidenceUrl, isLoadingUrl, urlError, awaitingCensura }) {
  const status = detection.evidence_status
  const chip = evidenceChip(status)

  // While the detection says "available" but we are still fetching the
  // presigned URL, show a small skeleton in place of the player to avoid
  // a flicker between empty <audio> and ready <audio>.
  if (status === 'available' && (isLoadingUrl || !evidenceUrl) && !urlError) {
    return (
      <div className="dd-panel">
        <div className="dd-panel-head">
          <span className="dd-panel-title">Evidência</span>
          <span className={`dd-chip ${chip.tone}`}>{chip.label}</span>
        </div>
        <div className="dd-evidence-player-wrap">
          <Sk h={40} r="var(--radius-md)" />
          <Sk w={200} h={12} />
        </div>
        <Sk w={160} h={36} r="var(--radius-md)" />
      </div>
    )
  }

  if (status === 'pending' || status === 'generating') {
    return (
      <div className="dd-panel">
        <div className="dd-panel-head">
          <span className="dd-panel-title">Evidência</span>
          <span className={`dd-chip ${chip.tone}`}>{chip.label}</span>
        </div>
        <div className="dd-evidence-state">
          <div className="dd-spinner" />
          <span className="dd-evidence-state-title">Gerando evidência…</span>
          <span className="dd-evidence-state-desc">
            O áudio está sendo processado pelo worker e ficará disponível em alguns segundos.
          </span>
        </div>
      </div>
    )
  }

  if (status === 'failed' || status === 'missing' || urlError) {
    const awaiting = awaitingCensura && status !== 'failed'
    return (
      <div className="dd-panel">
        <div className="dd-panel-head">
          <span className="dd-panel-title">Evidência</span>
          <span className={`dd-chip ${awaiting ? 'is-warning' : chip.tone}`}>
            {awaiting ? 'Aguardando censura' : chip.label}
          </span>
        </div>
        <div className="dd-evidence-state">
          <span className="dd-evidence-state-title">
            {status === 'failed'
              ? 'Falha ao gerar evidência'
              : awaiting
              ? 'Aguardando censura da emissora'
              : 'Evidência indisponível'}
          </span>
          <span className="dd-evidence-state-desc">
            {status === 'failed'
              ? 'O encoder retornou erro durante a geração do clip. A detecção em si permanece válida — apenas o áudio não pôde ser preservado.'
              : awaiting
              ? 'Esta veiculação foi registrada pelo comprovante; o áudio da censura ainda não foi anexado.'
              : 'Este registro não possui clip de áudio armazenado. Detecções recentes aparecem como “Pendente” por alguns segundos antes de serem encodadas.'}
          </span>
        </div>
      </div>
    )
  }

  return (
    <div className="dd-panel">
      <div className="dd-panel-head">
        <span className="dd-panel-title">Evidência</span>
        <span className={`dd-chip ${chip.tone}`}>{chip.label}</span>
      </div>

      <div className="dd-evidence-player-wrap">
        <audio
          className="dd-audio-native"
          src={evidenceUrl ?? ''}
          controls
          preload="metadata"
        />
        <div className="dd-evidence-meta">
          <span>Tamanho: <strong>{formatBytes(detection.evidence_size_bytes)}</strong></span>
          {detection.evidence_key && (
            <span title={detection.evidence_key} style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: 280 }}>
              <span style={{ color: 'var(--c-text-3)' }}>Chave: </span>
              <code style={{ fontSize: 11 }}>{detection.evidence_key}</code>
            </span>
          )}
        </div>
      </div>

      <div className="dd-evidence-actions">
        <a
          className="dd-download-btn"
          href={evidenceUrl ?? '#'}
          download={`detection-${detection.id}.m4a`}
          target="_blank"
          rel="noopener noreferrer"
        >
          <DownloadIcon /> Baixar áudio
        </a>
      </div>
    </div>
  )
}

// ProofCard: comprovante PDF do lote (manual_proof_batches). Admin-only. Abre o
// presigned em nova aba. Renderizado só quando a detecção tem proof_batch_id.
function ProofCard({ query }) {
  const url = query.data?.url
  const ready = !!url && !query.isLoading && !query.error
  return (
    <div className="dd-panel">
      <div className="dd-panel-head">
        <span className="dd-panel-title">Comprovante</span>
        <span className="dd-chip is-success">PDF</span>
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <span style={{ fontSize: 13, color: 'var(--c-text-2)', lineHeight: 1.5 }}>
          Documento enviado pela emissora comprovando as veiculações deste lote.
        </span>
        <a
          className="dd-download-btn"
          href={ready ? url : '#'}
          target="_blank"
          rel="noopener noreferrer"
          aria-disabled={!ready}
          style={ready ? undefined : { pointerEvents: 'none', opacity: 0.55 }}
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
            <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" /><path d="M14 2v6h6" />
          </svg>
          {query.isLoading ? 'Carregando…' : query.error ? 'Indisponível' : 'Ver comprovante (PDF)'}
        </a>
      </div>
    </div>
  )
}

// CensuraUploader: anexa o áudio da censura a uma detecção sem áudio (POST
// /detections/:id/evidence). Admin-only. Ao subir, a query da detecção é
// invalidada, evidence_status vira 'available' e o player aparece.
function CensuraUploader({ detection }) {
  const inputRef = useRef(null)
  const [file, setFile] = useState(null)
  const [err, setErr] = useState('')
  const upload = useUploadDetectionEvidence()
  const MAX_MB = 25
  const MIME = ['audio/mpeg', 'audio/mp3', 'audio/mp4', 'audio/x-m4a', 'audio/aac', 'audio/wav', 'audio/x-wav', 'audio/wave', 'audio/ogg']

  function pick(f) {
    setErr('')
    if (!f) { setFile(null); return }
    if (f.size > MAX_MB * 1024 * 1024) { setErr(`Acima de ${MAX_MB}MB.`); setFile(null); return }
    if (f.type && !MIME.includes(f.type.toLowerCase())) { setErr('Formato inválido (mp3, m4a, wav, aac, ogg).'); setFile(null); return }
    setFile(f)
  }

  async function submit() {
    if (!file) { inputRef.current?.click(); return }
    try {
      await upload.mutateAsync({ id: detection.id, audio: file })
      setFile(null)
    } catch (e) {
      const s = e?.response?.status
      window.alert(
        s === 409 ? 'Essa veiculação já tem censura.'
        : s === 415 ? 'Formato de áudio não suportado.'
        : s === 413 ? 'Áudio acima de 25MB.'
        : 'Falha ao subir a censura. Tente novamente.'
      )
    }
  }

  return (
    <div className="dd-panel">
      <div className="dd-panel-head">
        <span className="dd-panel-title">Subir censura</span>
        <span className="dd-chip is-warning">aguardando áudio</span>
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <span style={{ fontSize: 13, color: 'var(--c-text-2)', lineHeight: 1.5 }}>
          Quando a emissora mandar o áudio da censura, anexe aqui. A veiculação já conta nos relatórios; isso adiciona o player de evidência.
        </span>
        <input
          ref={inputRef}
          type="file"
          accept="audio/mpeg,audio/mp3,audio/mp4,audio/x-m4a,audio/aac,audio/wav,audio/x-wav,audio/ogg,.mp3,.m4a,.wav,.aac,.ogg"
          style={{ display: 'none' }}
          onChange={e => pick(e.target.files?.[0] ?? null)}
        />
        {file && (
          <span style={{
            display: 'inline-flex', alignItems: 'center', gap: 8, alignSelf: 'flex-start', maxWidth: '100%',
            padding: '7px 12px', borderRadius: 'var(--radius-full)',
            background: '#f0fdf4', border: '1px solid var(--c-success)',
            color: 'var(--c-success)', fontSize: 12, fontWeight: 600,
          }}>
            <span style={{ whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{file.name}</span>
            <span style={{ opacity: 0.75, flexShrink: 0 }}>· {(file.size / 1024 / 1024).toFixed(1)}MB</span>
          </span>
        )}
        {err && <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--c-danger)' }}>{err}</span>}
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          <button type="button" className="btn btn-secondary" onClick={() => inputRef.current?.click()} disabled={upload.isPending}>
            {file ? 'Trocar arquivo' : 'Escolher arquivo'}
          </button>
          <button type="button" className="btn btn-primary" onClick={submit} disabled={upload.isPending || !file}>
            {upload.isPending ? 'Enviando…' : 'Subir censura'}
          </button>
        </div>
      </div>
    </div>
  )
}

function AnalysisPanel({ detection }) {
  const conf = detection.confidence ?? 0
  const cov  = detection.temporal_coverage ?? 0
  const confTone = confidenceTone(conf)
  const covTone  = cov >= 0.8 ? 'is-action' : cov >= 0.5 ? 'is-warning' : 'is-danger'
  const auditCov = detection.audit_coverage
  const auditCovTone = auditCov >= 0.4 ? 'is-action' : auditCov >= 0.15 ? 'is-warning' : 'is-danger'

  return (
    <div className="dd-panel">
      <div className="dd-panel-head">
        <span className="dd-panel-title">Análise técnica</span>
        <span className="dd-chip is-muted">id #{detection.id}</span>
      </div>

      <div className="dd-metrics">
        <MetricBar
          label="Confiança"
          value={conf}
          tone={confTone}
          display={`${(conf * 100).toFixed(2)}%`}
        />
        <MetricBar
          label="Cobertura temporal"
          value={cov}
          tone={covTone}
          display={`${(cov * 100).toFixed(1)}%`}
        />
        {auditCov != null && (
          <MetricBar
            label="Cobertura do áudio (§9.9)"
            value={auditCov}
            tone={auditCovTone}
            display={`${(auditCov * 100).toFixed(1)}%`}
          />
        )}

        <div className="dd-metric-grid">
          <div className="dd-mini-card">
            <span className="dd-mini-card-label">Hashes casados</span>
            <span className="dd-mini-card-value">
              {detection.hash_count != null
                ? detection.hash_count.toLocaleString('pt-BR')
                : '—'}
            </span>
            <span className="dd-mini-card-sub">picos pareados confirmados</span>
          </div>
          <div className="dd-mini-card">
            <span className="dd-mini-card-label">Variante / Rate</span>
            <span className="dd-mini-card-value">
              v{detection.variant_used ?? 0} · {formatRate(detection.rate_used)}
            </span>
            <span className="dd-mini-card-sub">pipeline multi-rate</span>
          </div>
        </div>

        <div className="dd-metric">
          <span className="dd-metric-label">Janela de match</span>
          <div className="dd-window-display">
            <div className="dd-window-range">
              <span>{formatOffsetMs(detection.match_start_offset_ms)}</span>
              <span className="dd-window-range-arrow">→</span>
              <span>{formatOffsetMs(detection.match_end_offset_ms)}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

// ── Main page ───────────────────────────────────────────────────

export default function DetectionDetailPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { isAdmin } = useAuth()
  const confirm = useConfirm()
  const ignoreDetection  = useIgnoreDetection()
  const restoreDetection = useRestoreDetection()

  // Detection itself
  const detectionQuery = useQuery({
    queryKey: ['detection', id],
    queryFn:  () => api.get(`/detections/${id}`).then(r => r.data),
    retry: (failureCount, error) => {
      if (error?.response?.status === 404) return false
      return failureCount < 2
    },
  })

  const detection = detectionQuery.data
  const stationId    = detection?.station_id
  const commercialId = detection?.commercial_id
  const campaignId   = detection?.campaign_id

  // Station context
  const stationQuery = useQuery({
    queryKey: ['station', stationId],
    queryFn:  () => api.get(`/stations/${stationId}`).then(r => r.data),
    enabled:  !!stationId,
  })

  // Commercial context
  const commercialQuery = useQuery({
    queryKey: ['commercial', commercialId],
    queryFn:  () => api.get(`/commercials/${commercialId}`).then(r => r.data),
    enabled:  !!commercialId,
  })

  // Campaigns list (resolve campaign by id)
  const campaignsQuery = useQuery({
    queryKey: ['campaigns'],
    queryFn:  () => api.get('/campaigns').then(r => r.data.data ?? []),
    enabled:  !!campaignId,
    staleTime: 60_000,
  })

  // Evidence presigned URL — only fetched when status === 'available'.
  const evidenceUrlQuery = useQuery({
    queryKey: ['detection-evidence-url', id],
    queryFn:  () => api.get(`/detections/${id}/evidence/url`).then(r => r.data),
    enabled:  detection?.evidence_status === 'available',
    // Presigned URL TTL is 5 minutes server-side; refresh slightly before
    staleTime: 4 * 60 * 1000,
    retry: 1,
  })

  // Comprovante PDF do lote (manual_proof_batches). Admin-only — o endpoint
  // /proof/url vive no grupo admin. Habilita só quando há proof_batch_id.
  const proofUrlQuery = useQuery({
    queryKey: ['detection-proof-url', id],
    queryFn:  () => api.get(`/detections/${id}/proof/url`).then(r => r.data),
    enabled:  isAdmin && !!detection?.proof_batch_id,
    staleTime: 4 * 60 * 1000,
    retry: 1,
  })

  // Refresh the URL automatically when it expires while the user is still
  // on the page. We compute the timeout from `expires_at` and re-trigger.
  const [, setRefetchTick] = useState(0)
  useEffect(() => {
    const expiresAt = evidenceUrlQuery.data?.expires_at
    if (!expiresAt) return
    const ms = new Date(expiresAt).getTime() - Date.now() - 10_000 // refresh 10s early
    if (ms <= 0) {
      evidenceUrlQuery.refetch()
      return
    }
    const t = setTimeout(() => {
      evidenceUrlQuery.refetch()
      setRefetchTick(x => x + 1)
    }, ms)
    return () => clearTimeout(t)
  }, [evidenceUrlQuery.data?.expires_at, evidenceUrlQuery])

  const campaign = useMemo(() => {
    if (!campaignId || !campaignsQuery.data) return null
    return campaignsQuery.data.find(c => c.id === campaignId) ?? null
  }, [campaignId, campaignsQuery.data])

  // ── Loading ─────────────────────────────────────────────────
  if (detectionQuery.isLoading) return <PageSkeleton />

  // ── 404 / not found ─────────────────────────────────────────
  const status = detectionQuery.error?.response?.status
  if (status === 404 || (!detectionQuery.isLoading && !detection)) {
    return (
      <div className="dd-empty">
        <div className="dd-empty-icon-wrap">
          <SearchIcon size={42} />
        </div>
        <div className="dd-empty-title">Detecção não encontrada</div>
        <p className="dd-empty-desc">
          O registro que você tentou abrir não existe ou foi removido. Volte para a lista
          de veiculações para localizar outras detecções recentes.
        </p>
        <button className="btn btn-primary" onClick={() => navigate('/detections')}>
          Voltar para Veiculações
        </button>
        <div className="dd-ghost" aria-hidden="true">
          <div className="dd-ghost-block" />
          <div className="dd-ghost-block" />
          <div className="dd-ghost-block" />
          <div className="dd-ghost-block" />
        </div>
      </div>
    )
  }

  // Generic error fallback (network, 5xx)
  if (detectionQuery.error) {
    return (
      <div className="dd-empty">
        <div className="dd-empty-icon-wrap" style={{ background: 'color-mix(in srgb, var(--c-danger) 12%, transparent)', color: 'var(--c-danger)' }}>
          <WarnIcon />
        </div>
        <div className="dd-empty-title">Erro ao carregar detecção</div>
        <p className="dd-empty-desc">
          Não foi possível obter os dados desta detecção. Verifique sua conexão e tente novamente.
        </p>
        <button className="btn btn-secondary" onClick={() => detectionQuery.refetch()}>
          Tentar novamente
        </button>
      </div>
    )
  }

  // ── Loaded ──────────────────────────────────────────────────
  const station    = stationQuery.data
  const commercial = commercialQuery.data
  const evidenceUrl = evidenceUrlQuery.data?.url ?? null
  const hasAudio = detection.evidence_status === 'available' || !!detection.evidence_key
  // Manual/lote sem áudio = "aguardando censura": estado neutro, não erro vermelho.
  const awaitingCensura = !hasAudio && (!!detection.manual_at || !!detection.proof_batch_id)

  const stationCity = station?.city
  const stationState = station?.state
  const stationPlace = stationCity && stationState
    ? `${stationCity} / ${stationState}`
    : stationCity ?? stationState ?? ''
  const stationFreq  = station?.frequency_mhz != null
    ? `${station.band ?? ''}${station.band ? ' · ' : ''}${station.frequency_mhz} MHz`
    : station?.band ?? ''

  return (
    <div className="dd-root">

      {/* ── Header ── */}
      <div className="dd-header">
        <button className="dd-back-btn" onClick={() => navigate('/detections')} title="Voltar" aria-label="Voltar">
          <BackIcon />
        </button>
        <div className="dd-header-text">
          <span className="dd-eyebrow">Detecção · #{detection.id}</span>
          <h1 className="dd-title">{detection.commercial_name || 'Comercial sem título'}</h1>
          <span className="dd-subtitle">
            {detection.station_name}{detection.station_name ? ' · ' : ''}{formatLongDateTime(detection.detected_at)}
          </span>
        </div>
      </div>

      {/* ── Manual entry ribbon ── */}
      {detection.manual_at && (
        <div className="dd-manual-ribbon" role="status">
          <div className="dd-manual-ribbon-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
              <path d="M12 20h9" />
              <path d="M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z" />
            </svg>
          </div>
          <div className="dd-manual-ribbon-text">
            <span className="dd-manual-ribbon-title">
              Veiculação inserida manualmente em {formatShortDateTime(detection.manual_at)}
            </span>
            {detection.manual_note && (
              <span className="dd-manual-ribbon-desc">
                {detection.manual_note}
              </span>
            )}
            {!detection.manual_note && (
              <span className="dd-manual-ribbon-desc">
                Registro retroativo criado por um administrador. Conta normalmente em
                relatórios e agregados; o áudio não está disponível porque não houve
                captura automática.
              </span>
            )}
          </div>
        </div>
      )}

      {/* ── Retracted ribbon ── */}
      {detection.retracted_at && (
        <div className="dd-retracted-ribbon" role="alert">
          <div className="dd-retracted-ribbon-icon">
            <WarnIcon />
          </div>
          <div className="dd-retracted-ribbon-text">
            <span className="dd-retracted-ribbon-title">
              Detecção retraída em {formatShortDateTime(detection.retracted_at)}
            </span>
            <span className="dd-retracted-ribbon-desc">
              Provavelmente um corte mais longo (60s) sobrepôs este (30s) e o sistema
              consolidou a veiculação na versão mais completa.
            </span>
          </div>
        </div>
      )}

      {/* ── Ignored ribbon ── */}
      {detection.ignored_at && (
        <div className="dd-ignored-ribbon" role="alert">
          <div className="dd-ignored-ribbon-icon">
            <WarnIcon />
          </div>
          <div className="dd-ignored-ribbon-text">
            <span className="dd-ignored-ribbon-title">
              Veiculação desconsiderada em {formatShortDateTime(detection.ignored_at)}
            </span>
            <span className="dd-ignored-ribbon-desc">
              Esta veiculação foi marcada como desconsiderada por um administrador.
              Ela não conta em relatórios nem agregados, mas o áudio e os dados
              originais permanecem preservados para auditoria.
            </span>
          </div>
        </div>
      )}

      {/* ── Script block ── only when the material has copy registered.
            Lives above the technical grid because it's the most "human"
            content on the page — what was actually spoken in the spot. */}
      {detection.commercial_script && (
        <div className="dd-script">
          <div className="dd-script-head">
            <div className="dd-script-icon">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                <path d="M14 2v6h6" />
                <path d="M8 13h8M8 17h6" />
              </svg>
            </div>
            <span className="dd-script-eyebrow">Texto do comercial</span>
          </div>
          <p className="dd-script-body">{detection.commercial_script}</p>
        </div>
      )}

      {/* ── Main grid: Evidence | Analysis ── */}
      <div className="dd-grid">
        <EvidencePanel
          detection={detection}
          evidenceUrl={evidenceUrl}
          isLoadingUrl={evidenceUrlQuery.isLoading}
          urlError={!!evidenceUrlQuery.error}
          awaitingCensura={awaitingCensura}
        />
        <AnalysisPanel detection={detection} />
      </div>

      {/* ── Admin: comprovante PDF + subir censura ── */}
      {isAdmin && (detection.proof_batch_id || !hasAudio) && (
        <div style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fit, minmax(280px, 1fr))',
          gap: 16, marginTop: 16,
        }}>
          {detection.proof_batch_id && <ProofCard query={proofUrlQuery} />}
          {!hasAudio && <CensuraUploader detection={detection} />}
        </div>
      )}

      {/* ── Context cards ── */}
      <div className="dd-context">

        {/* Station */}
        {stationId && (
          <Link className="dd-context-card" to={`/stations/${stationId}/edit`}>
            <div className="dd-context-card-head">
              <span>Estação</span>
              <span className="dd-context-card-head-arrow"><ChevronRightIcon /></span>
            </div>
            <div className="dd-context-card-body">
              {station ? (
                <StationAvatar station={station} size={44} />
              ) : (
                <Sk w={44} h={44} r={8} />
              )}
              <div className="dd-context-card-text">
                <span className="dd-context-card-name">
                  {station?.name ?? detection.station_name ?? <Sk w={140} h={14} />}
                </span>
                <span className="dd-context-card-meta">
                  {station ? (stationFreq || '—') : <Sk w={110} h={11} />}
                </span>
                {stationPlace && (
                  <span className="dd-context-card-meta">{stationPlace}</span>
                )}
              </div>
            </div>
          </Link>
        )}

        {/* Commercial */}
        <Link className="dd-context-card" to="/campaigns">
          <div className="dd-context-card-head">
            <span>Comercial</span>
            <span className="dd-context-card-head-arrow"><ChevronRightIcon /></span>
          </div>
          <div className="dd-context-card-body">
            <div className="dd-comm-icon"><MegaphoneIcon /></div>
            <div className="dd-context-card-text">
              <span className="dd-context-card-name">
                {commercial?.title ?? detection.commercial_name ?? <Sk w={160} h={14} />}
              </span>
              <span className="dd-context-card-meta">
                {commercial ? (
                  <span className="dd-chip-row">
                    {commercial.cut_label && <span className="dd-chip">{commercial.cut_label}</span>}
                    {commercial.duration_seconds != null && (
                      <span className="dd-chip">{commercial.duration_seconds}s</span>
                    )}
                    {commercial.fingerprint_status && (
                      <span className={`dd-chip ${commercial.fingerprint_status === 'ready' ? 'is-success' : commercial.fingerprint_status === 'failed' ? 'is-danger' : 'is-warning'}`}>
                        fp: {commercial.fingerprint_status}
                      </span>
                    )}
                  </span>
                ) : <Sk w={140} h={14} />}
              </span>
            </div>
          </div>
          {commercial?.fingerprint_hash_count != null && (
            <div className="dd-context-card-stat">
              <span className="dd-context-card-stat-label">Hashes do master</span>
              <span className="dd-context-card-stat-value">
                {commercial.fingerprint_hash_count.toLocaleString('pt-BR')}
              </span>
            </div>
          )}
        </Link>

        {/* Campaign */}
        <Link className="dd-context-card" to="/campaigns">
          <div className="dd-context-card-head">
            <span>Campanha</span>
            <span className="dd-context-card-head-arrow"><ChevronRightIcon /></span>
          </div>
          <div className="dd-context-card-body">
            <div className="dd-camp-icon"><CalendarIcon /></div>
            <div className="dd-context-card-text">
              <span className="dd-context-card-name">
                {campaign?.name ?? (campaignsQuery.isLoading ? <Sk w={150} h={14} /> : '—')}
              </span>
              {campaign?.client_name && (
                <span className="dd-context-card-meta">{campaign.client_name}</span>
              )}
              {(campaign?.start_date || campaign?.end_date) && (
                <span className="dd-context-card-meta">
                  {formatShortDate(campaign.start_date)} — {formatShortDate(campaign.end_date)}
                </span>
              )}
            </div>
          </div>
          {campaign?.status && (
            <div className="dd-context-card-stat">
              <span className="dd-context-card-stat-label">Status</span>
              <span className="dd-context-card-stat-value" style={{ textTransform: 'capitalize' }}>
                {campaign.status}
              </span>
            </div>
          )}
        </Link>

      </div>

      {/* ── Danger zone (admin) ── */}
      {isAdmin && (
        <DangerZone
          isIgnored={!!detection.ignored_at}
          isPending={ignoreDetection.isPending || restoreDetection.isPending}
          onIgnore={async () => {
            const ok = await confirm(
              'Desconsiderar esta veiculação?\n\n' +
              'Ela não vai mais contar em relatórios nem agregados até ser ' +
              'reativada. O áudio e os dados originais permanecem armazenados. ' +
              'Você pode reverter a qualquer momento.')
            if (!ok) return
            try { await ignoreDetection.mutateAsync(detection.id) }
            catch { window.alert('Erro ao desconsiderar. Tente novamente.') }
          }}
          onRestore={async () => {
            try { await restoreDetection.mutateAsync(detection.id) }
            catch { window.alert('Erro ao reativar. Tente novamente.') }
          }}
        />
      )}
    </div>
  )
}

// Admin-only soft-delete action. Lives at the bottom of the page, framed as a
// "zona de perigo" so it doesn't sit next to non-destructive actions. The
// button text + tone flips between "Desconsiderar" and "Reativar" based on
// the current ignored_at state.
function DangerZone({ isIgnored, isPending, onIgnore, onRestore }) {
  return (
    <div className="dd-danger-zone">
      <div className="dd-danger-zone-text">
        <span className="dd-danger-zone-title">
          {isIgnored ? 'Veiculação desconsiderada' : 'Zona de perigo'}
        </span>
        <span className="dd-danger-zone-desc">
          {isIgnored
            ? 'Esta veiculação está atualmente fora dos agregados. Reative-a se foi ' +
              'desconsiderada por engano ou se a situação que motivou a remoção ' +
              'voltou a ser válida.'
            : 'Desconsidere esta veiculação se ela não deve contar em relatórios — ' +
              'por exemplo, comerciais inseridos fora do contrato após acordo ' +
              'offline com a emissora, ou capturas acidentais. O áudio e os dados ' +
              'continuam armazenados; a ação é reversível.'}
        </span>
      </div>
      <button
        type="button"
        className={`dd-danger-zone-btn${isIgnored ? ' is-restore' : ''}`}
        onClick={isIgnored ? onRestore : onIgnore}
        disabled={isPending}
      >
        {isPending
          ? 'Processando…'
          : isIgnored ? 'Reativar veiculação' : 'Desconsiderar veiculação'}
      </button>
    </div>
  )
}

// ── Tiny date helpers (local only) ───────────────────────────────

function formatShortDate(isoOrDate) {
  if (!isoOrDate) return '—'
  const d = isoOrDate instanceof Date ? isoOrDate : new Date(isoOrDate)
  if (isNaN(d.getTime())) return String(isoOrDate)
  return d.toLocaleDateString('pt-BR', { day: '2-digit', month: '2-digit', year: 'numeric', timeZone: TZ })
}
