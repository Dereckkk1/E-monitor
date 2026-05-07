import { useState, useRef, useEffect, useMemo, useCallback } from 'react'
import { createPortal } from 'react-dom'
import {
  useCampaigns, useCreateCampaign, useCancelCampaign, useDeleteCampaign,
  useClients, useStations, useCommercials, useUploadCommercial,
  useUpdateCommercialStations, useUpdateCampaignStations, useDeleteCommercial,
} from '../api/hooks'
import RSelect from '../components/RSelect'
import StationAvatar from '../components/StationAvatar'

// ─── Formatters ────────────────────────────────────────────────────────────────

function fmtDate(iso) {
  if (!iso) return '—'
  const [y, m, d] = iso.slice(0, 10).split('-')
  return `${d}/${m}/${y}`
}

function fmtDur(sec) {
  if (!sec) return '—'
  return `${sec.toFixed(1)}s`
}

// daysUntil returns the (signed) integer number of days between today and
// the given ISO date. Negative => already passed. Compares date-only,
// observador local; suficiente para tooltip humano.
function daysUntil(iso) {
  if (!iso) return null
  const target = new Date(iso.slice(0, 10) + 'T00:00:00')
  const today  = new Date()
  today.setHours(0, 0, 0, 0)
  const ms = target - today
  return Math.round(ms / 86400000)
}

// dateTooltip builds the hover hint per the lifecycle UX:
//   "começa em X dias" / "começou há X dias"
//   "termina em Y dias" / "encerrou em Y" / "encerra hoje"
function startDateTooltip(iso, status) {
  if (!iso) return ''
  const d = daysUntil(iso)
  if (d == null) return ''
  if (status === 'programada') {
    if (d <= 0) return 'inicia hoje'
    return d === 1 ? 'começa amanhã' : `começa em ${d} dias`
  }
  if (d === 0) return 'iniciou hoje'
  if (d < 0)  return `iniciou há ${-d} ${-d === 1 ? 'dia' : 'dias'}`
  return d === 1 ? 'inicia amanhã' : `inicia em ${d} dias`
}

function endDateTooltip(iso, status) {
  if (!iso) return ''
  const d = daysUntil(iso)
  if (d == null) return ''
  if (status === 'concluida' || status === 'cancelada') {
    if (d === 0)  return 'encerrou hoje'
    if (d < 0)    return `encerrou há ${-d} ${-d === 1 ? 'dia' : 'dias'}`
    return `prevista para ${d} ${d === 1 ? 'dia' : 'dias'}`
  }
  if (d === 0) return 'encerra hoje'
  if (d < 0)   return `encerrou há ${-d} ${-d === 1 ? 'dia' : 'dias'}`
  return d === 1 ? 'termina amanhã' : `termina em ${d} dias`
}

// ─── Constants ─────────────────────────────────────────────────────────────────

// Lifecycle PT-BR (§18.2.1) — 4 estados finitos.
const STATUS_LABEL = {
  programada: 'Programada',
  ativa: 'Ativa',
  concluida: 'Concluída',
  cancelada: 'Cancelada',
}
const STATUS_CLASS = {
  programada: 'badge-programada',
  ativa: 'badge-ativa',
  concluida: 'badge-concluida',
  cancelada: 'badge-cancelada',
}
// Ordem de exibição: ativas → programadas (próximas a entrar) → concluídas/canceladas (histórico).
const STATUS_ORDER = { ativa: 1, programada: 2, concluida: 3, cancelada: 4 }
const FP_LABEL    = { pending: 'aguardando', generating: 'gerando…', ready: 'pronto', failed: 'falhou' }
const FP_CLASS    = { pending: 'fp-pending', generating: 'fp-generating', ready: 'fp-ready', failed: 'fp-failed' }

// ─── Small icons ───────────────────────────────────────────────────────────────

function IconTrash() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
      <path d="M2 3.5H12M5.5 3.5V2.5C5.5 2.22 5.72 2 6 2H8C8.28 2 8.5 2.22 8.5 2.5V3.5M5.5 6.5V10.5M8.5 6.5V10.5M3 3.5L3.5 11.5C3.5 11.78 3.72 12 4 12H10C10.28 12 10.5 11.78 10.5 11.5L11 3.5" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round"/>
    </svg>
  )
}

function IconChevron({ open }) {
  return (
    <svg className={`campaign-chevron${open ? ' open' : ''}`} width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
      <path d="M5 3L9 7L5 11" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

function IconSpinner() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" style={{ animation: 'spin 1s linear infinite', flexShrink: 0 }} aria-hidden="true">
      <circle cx="7" cy="7" r="5.5" stroke="currentColor" strokeWidth="1.5" strokeDasharray="20 15" strokeLinecap="round" />
    </svg>
  )
}

function IconUpload() {
  return (
    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path d="M12 3V15M12 3L8 7M12 3L16 7" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
      <path d="M3 17V19C3 20.1 3.9 21 5 21H19C20.1 21 21 20.1 21 19V17" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
    </svg>
  )
}

function IconX({ size = 12 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 12 12" fill="none" aria-hidden="true">
      <path d="M2 2L10 10M10 2L2 10" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
    </svg>
  )
}

function IconPlus({ size = 14 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 14 14" fill="none" aria-hidden="true">
      <path d="M7 2V12M2 7H12" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
    </svg>
  )
}

function IconMegaphone() {
  return (
    <svg width="48" height="48" viewBox="0 0 48 48" fill="none" aria-hidden="true">
      <path d="M40 8C40 8 30 15 18 16.5H12C10.3 16.5 9 17.8 9 19.5V28.5C9 30.2 10.3 31.5 12 31.5H18L21 43H27L24 31.5C33 33 40 40 40 40V8Z" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" fill="none"/>
      <path d="M43 24C43 24 43 19.5 40 16.5" stroke="currentColor" strokeWidth="2" strokeLinecap="round"/>
      <path d="M43 24C43 24 43 28.5 40 31.5" stroke="currentColor" strokeWidth="2" strokeLinecap="round"/>
    </svg>
  )
}

function IconAudio() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path d="M5 3H3C2.45 3 2 3.45 2 4V12C2 12.55 2.45 13 3 13H5L9 15V1L5 3Z" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round"/>
      <path d="M12 5.5C13.1 6.3 13.8 7.1 13.8 8C13.8 8.9 13.1 9.7 12 10.5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/>
    </svg>
  )
}

function IconPlay() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
      <path d="M3.5 2.5V11.5L11 7L3.5 2.5Z" fill="currentColor" />
    </svg>
  )
}

function IconStop() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
      <rect x="3" y="3" width="8" height="8" rx="1" fill="currentColor" />
    </svg>
  )
}

function IconDownload() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
      <path d="M7 2V9M7 9L4 6M7 9L10 6" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"/>
      <path d="M2.5 11H11.5" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round"/>
    </svg>
  )
}

// ─── Helpers ───────────────────────────────────────────────────────────────────

function stationDialLine(s) {
  const dial = [s.band, s.frequency_mhz != null ? s.frequency_mhz : null].filter(Boolean).join(' ')
  return [dial, s.city].filter(Boolean).join(' · ')
}

// ─── StreamSignal ──────────────────────────────────────────────────────────────

function streamSignalState(station) {
  if (station.monitoring_status !== 'active') return null
  const lhc = station.last_health_check
  if (lhc) {
    const ageMins = (Date.now() - new Date(lhc).getTime()) / 60000
    if (ageMins < 2)  return { cls: 'ok',   label: 'Ao vivo' }
    if (ageMins < 5)  return { cls: 'warn', label: `${Math.round(ageMins)}min sem sinal` }
    return { cls: 'dead', label: 'Sem sinal' }
  }
  // Sem heartbeat algum: stream nunca conectou. Mostra "Iniciando…" só nos
  // primeiros 3 minutos após o worker subir — depois disso é falha real
  // (URL inválida, servidor caído, bloqueio). Anchor: updated_at, que o
  // supervisor bumpa ao chamar UpdateMonitoringStatus('active').
  const updated = station.updated_at ? new Date(station.updated_at).getTime() : 0
  const startAgeMins = updated ? (Date.now() - updated) / 60000 : Infinity
  if (startAgeMins < 3) return { cls: 'init', label: 'Iniciando…' }
  return { cls: 'dead', label: 'Sem sinal' }
}

function StreamSignal({ station }) {
  const sig = streamSignalState(station)
  if (!sig) return null
  return (
    <span className={`stream-signal stream-signal-${sig.cls}`}>
      <span className="stream-signal-dot" />
      {sig.label}
    </span>
  )
}

// ─── StationPickerDropdown ─────────────────────────────────────────────────────
// Rendered via portal to escape table overflow clipping.

function StationPickerDropdown({ commercial, campaignStationIds, allStations, anchorRect, onClose }) {
  const updateStations = useUpdateCommercialStations()
  const current = useMemo(() => new Set(commercial.target_stations ?? []), [commercial.target_stations])
  const [selected, setSelected] = useState(current)
  const dropRef = useRef(null)

  // Position: prefer below the trigger, flip up if not enough space
  const style = useMemo(() => {
    if (!anchorRect) return {}
    const dropH = 320
    const spaceBelow = window.innerHeight - anchorRect.bottom
    const top = spaceBelow >= dropH
      ? anchorRect.bottom + window.scrollY + 4
      : anchorRect.top + window.scrollY - dropH - 4
    const left = Math.min(anchorRect.left + window.scrollX, window.innerWidth - 356)
    return { top, left: Math.max(8, left) }
  }, [anchorRect])

  // Close on outside click or Escape
  useEffect(() => {
    function onMouseDown(e) {
      if (dropRef.current && !dropRef.current.contains(e.target)) onClose()
    }
    function onKey(e) { if (e.key === 'Escape') onClose() }
    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onMouseDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [onClose])

  const campaignStations = useMemo(
    () => campaignStationIds.map(id => allStations.find(s => s.id === id)).filter(Boolean),
    [campaignStationIds, allStations]
  )

  function toggle(id) {
    setSelected(prev => {
      const next = new Set(prev)
      next.has(id) ? next.delete(id) : next.add(id)
      return next
    })
  }

  function save() {
    updateStations.mutate({
      id: commercial.id,
      campaignId: commercial.campaign_id,
      targetStations: [...selected],
    }, { onSuccess: onClose })
  }

  return createPortal(
    <div className="station-picker-dropdown" ref={dropRef} style={{ position: 'fixed', ...style }}>
      <div className="station-picker-header">
        <span className="station-picker-title">Emissoras deste material</span>
        <button className="station-picker-close" onClick={onClose} type="button"><IconX size={14} /></button>
      </div>

      {campaignStations.length === 0 ? (
        <p className="station-picker-empty">Adicione emissoras à campanha primeiro.</p>
      ) : (
        <div className="station-picker-list">
          {campaignStations.map(s => {
            const dial = stationDialLine(s)
            return (
              <button
                key={s.id}
                className={`station-picker-item${selected.has(s.id) ? ' selected' : ''}`}
                onClick={() => toggle(s.id)}
                type="button"
              >
                <StationAvatar station={s} size={28} />
                <div className="station-picker-item-info">
                  <span className="station-picker-item-name">{s.name}</span>
                  {dial && <span className="station-picker-item-dial">{dial}</span>}
                </div>
                {selected.has(s.id) && <span className="station-picker-item-check">✓</span>}
              </button>
            )
          })}
        </div>
      )}

      <p className="station-picker-hint">
        {selected.size === 0
          ? 'Sem emissoras = comercial inativo (não será detectado)'
          : `${selected.size} emissora(s) selecionada(s)`}
      </p>

      <div className="station-picker-actions">
        <button className="btn btn-secondary btn-sm" onClick={() => setSelected(new Set())} type="button">Limpar</button>
        <button
          className="btn btn-primary btn-sm"
          onClick={save}
          disabled={updateStations.isPending}
          type="button"
        >
          {updateStations.isPending ? <><IconSpinner /> Salvando</> : 'Salvar'}
        </button>
      </div>
    </div>,
    document.body
  )
}

// ─── CommercialRow ─────────────────────────────────────────────────────────────

function CommercialRow({ commercial, campaignStationIds, allStations }) {
  const [anchorRect, setAnchorRect] = useState(null)
  const [playing, setPlaying] = useState(false)
  const btnRef = useRef(null)
  const audioRef = useRef(null)
  const deleteCommercial = useDeleteCommercial()

  function openPicker() {
    if (anchorRect) { setAnchorRect(null); return }
    setAnchorRect(btnRef.current?.getBoundingClientRect() ?? null)
  }

  async function handleDelete() {
    const ok = await window.confirm(
      `Excluir "${commercial.title}"? Esta ação remove o material e seus fingerprints. Não é possível desfazer.`
    )
    if (!ok) return
    deleteCommercial.mutate({ id: commercial.id, campaignId: commercial.campaign_id }, {
      onError: (err) => {
        const status = err?.response?.status
        if (status === 409) {
          window.alert('Este material já tem detecções registradas e não pode ser excluído.')
        } else {
          window.alert('Erro ao excluir material.')
        }
      }
    })
  }

  const audioSrc = `/v1/internal/commercials/${commercial.id}/audio`
  const downloadHref = `${audioSrc}?download=1`

  function togglePlay() {
    setPlaying(p => !p)
  }

  useEffect(() => {
    if (playing && audioRef.current) {
      audioRef.current.play().catch(() => setPlaying(false))
    }
  }, [playing])

  const assignedStations = useMemo(() => {
    const ids = commercial.target_stations ?? []
    return ids.map(id => allStations.find(s => s.id === id)).filter(Boolean)
  }, [commercial.target_stations, allStations])

  const isInactive = (commercial.target_stations ?? []).length === 0

  return (
    <>
      <tr className="commercial-row">
        <td>
          <div className="commercial-title-cell">
            <span className="commercial-icon"><IconAudio /></span>
            <span className="commercial-title">{commercial.title}</span>
            {commercial.cut_label && <span className="commercial-cut">{commercial.cut_label}</span>}
          </div>
        </td>
        <td className="text-muted">{fmtDur(commercial.duration_seconds)}</td>
        <td>
          <span className={FP_CLASS[commercial.fingerprint_status] ?? ''}>
            {FP_LABEL[commercial.fingerprint_status] ?? commercial.fingerprint_status}
          </span>
        </td>
        <td>
          <button
            ref={btnRef}
            className="commercial-stations-btn"
            onClick={openPicker}
            type="button"
            title="Gerenciar emissoras"
          >
            {isInactive ? (
              <span className="commercial-stations-inactive">Inativo</span>
            ) : (
              <div className="commercial-stations-chips">
                {assignedStations.slice(0, 2).map(s => {
                  const dial = stationDialLine(s)
                  return (
                    <span key={s.id} className="commercial-station-chip">
                      <strong>{s.name}</strong>
                      {dial && <span className="commercial-station-chip-meta"> · {dial}</span>}
                    </span>
                  )
                })}
                {assignedStations.length > 2 && (
                  <span className="commercial-stations-more">+{assignedStations.length - 2}</span>
                )}
              </div>
            )}
            <span className="commercial-stations-edit-hint">✎</span>
          </button>
          {anchorRect && (
            <StationPickerDropdown
              commercial={commercial}
              campaignStationIds={campaignStationIds}
              allStations={allStations}
              anchorRect={anchorRect}
              onClose={() => setAnchorRect(null)}
            />
          )}
        </td>
        <td>
          <div style={{ display: 'inline-flex', gap: 4 }}>
            <button
              className="btn btn-icon btn-sm"
              onClick={togglePlay}
              type="button"
              title={playing ? 'Parar' : 'Ouvir'}
              style={{ color: playing ? 'var(--c-action)' : 'var(--c-text-2)' }}
            >
              {playing ? <IconStop /> : <IconPlay />}
            </button>
            <a
              className="btn btn-icon btn-sm"
              href={downloadHref}
              title="Baixar"
              style={{ color: 'var(--c-text-2)', textDecoration: 'none' }}
            >
              <IconDownload />
            </a>
            <button
              className="btn btn-icon btn-danger-ghost btn-sm"
              onClick={handleDelete}
              type="button"
              title="Excluir material"
              disabled={deleteCommercial.isPending}
            >
              <IconTrash />
            </button>
          </div>
        </td>
      </tr>
      {playing && (
        <tr className="commercial-row commercial-player-row">
          <td colSpan={5} style={{ padding: '6px 12px', background: 'var(--c-surface-2)' }}>
            <audio
              ref={audioRef}
              src={audioSrc}
              controls
              onEnded={() => setPlaying(false)}
              style={{ width: '100%', height: 32 }}
            />
          </td>
        </tr>
      )}
    </>
  )
}

// ─── BulkUploadZone ────────────────────────────────────────────────────────────

function BulkUploadZone({ campaignId, campaignStationIds, allStations }) {
  const uploadCommercial   = useUploadCommercial()
  const updateStations     = useUpdateCommercialStations()
  const fileInputRef       = useRef(null)
  const [queue, setQueue]  = useState([])
  const [dragging, setDragging] = useState(false)

  const campaignStations = useMemo(
    () => campaignStationIds.map(id => allStations.find(s => s.id === id)).filter(Boolean),
    [campaignStationIds, allStations]
  )
  const hasStations = campaignStations.length > 0

  function addFiles(files) {
    const entries = Array.from(files)
      .filter(f => /\.(wav|mp3|m4a|aac|mpeg)$/i.test(f.name))
      .map(file => ({
        key: `${file.name}-${Date.now()}-${Math.random()}`,
        file,
        name: file.name.replace(/\.[^.]+$/, ''),
        cut: '',
        // null = não escolhido ainda (obrigatório); [] = todas; [ids…] = específicas
        stations: hasStations ? null : [],
        status: 'pending',
        error: null,
      }))
    setQueue(prev => [...prev, ...entries])
  }

  function onDrop(e) {
    e.preventDefault(); setDragging(false); addFiles(e.dataTransfer.files)
  }
  function onFileInput(e) { addFiles(e.target.files); e.target.value = '' }
  function removeEntry(key) { setQueue(prev => prev.filter(e => e.key !== key)) }
  function updateEntry(key, patch) {
    setQueue(prev => prev.map(e => e.key === key ? { ...e, ...patch } : e))
  }

  function toggleStation(key, stationId) {
    setQueue(prev => prev.map(e => {
      if (e.key !== key) return e
      const cur = e.stations ?? []
      const next = cur.includes(stationId)
        ? cur.filter(id => id !== stationId)
        : [...cur, stationId]
      return { ...e, stations: next }
    }))
  }

  function setAllStations(key) {
    updateEntry(key, { stations: [] })
  }

  async function submitAll() {
    const pending = queue.filter(e => e.status === 'pending')
    for (const entry of pending) {
      updateEntry(entry.key, { status: 'uploading' })
      const fd = new FormData()
      fd.append('campaign_id', campaignId)
      fd.append('title', entry.name.trim() || entry.file.name)
      if (entry.cut) fd.append('cut_label', entry.cut)
      fd.append('audio', entry.file)
      try {
        const created = await new Promise((resolve, reject) => {
          uploadCommercial.mutate(fd, { onSuccess: resolve, onError: reject })
        })
        // Se escolheu emissoras específicas, atualiza agora
        const stationIds = entry.stations ?? []
        if (stationIds.length > 0) {
          await new Promise((resolve, reject) => {
            updateStations.mutate(
              { id: created.id, campaignId, targetStations: stationIds },
              { onSuccess: resolve, onError: reject }
            )
          })
        }
        updateEntry(entry.key, { status: 'done' })
      } catch (err) {
        const msg = err?.response?.data || 'Erro no upload.'
        updateEntry(entry.key, { status: 'error', error: msg })
      }
    }
    setTimeout(() => setQueue(prev => prev.filter(e => e.status !== 'done')), 1200)
  }

  const pendingEntries  = queue.filter(e => e.status === 'pending')
  const canSubmit = pendingEntries.length > 0
    && pendingEntries.every(e => e.name.trim() && e.stations !== null)
    && !uploadCommercial.isPending

  return (
    <div className="bulk-upload-section">
      <div
        className={`bulk-upload-zone${dragging ? ' dragging' : ''}`}
        onDragOver={e => { e.preventDefault(); setDragging(true) }}
        onDragLeave={() => setDragging(false)}
        onDrop={onDrop}
        onClick={() => fileInputRef.current?.click()}
      >
        <input ref={fileInputRef} type="file" accept=".wav,.mp3,.m4a,.aac,.mpeg" multiple
          style={{ display: 'none' }} onChange={onFileInput} />
        <span className="bulk-upload-icon"><IconUpload /></span>
        <span className="bulk-upload-label">
          {dragging ? 'Solte os arquivos aqui' : 'Arraste arquivos ou clique para selecionar'}
        </span>
        <span className="bulk-upload-hint">.wav .mp3 .m4a .aac — vários arquivos aceitos</span>
      </div>

      {queue.length > 0 && (
        <div className="bulk-queue">
          {queue.map(entry => (
            <div key={entry.key} className={`bulk-queue-card status-${entry.status}`}>

              {/* Row 1: arquivo + nome + cut + status + remover */}
              <div className="bulk-queue-row1">
                <span className="bulk-queue-filename">
                  <IconAudio /><span title={entry.file.name}>{entry.file.name}</span>
                </span>
                <input
                  className="input bulk-queue-name"
                  placeholder="Nome do material *"
                  value={entry.name}
                  onChange={e => updateEntry(entry.key, { name: e.target.value })}
                  disabled={entry.status !== 'pending'}
                />
                <input
                  className="input bulk-queue-cut"
                  placeholder="Cut (ex: 30s)"
                  value={entry.cut}
                  onChange={e => updateEntry(entry.key, { cut: e.target.value })}
                  disabled={entry.status !== 'pending'}
                />
                <span className="bulk-queue-status">
                  {entry.status === 'uploading' && <IconSpinner />}
                  {entry.status === 'done'      && <span style={{ color: 'var(--c-success)' }}>✓</span>}
                  {entry.status === 'error'     && <span className="text-error" title={entry.error}>✗</span>}
                </span>
                {entry.status === 'pending' && (
                  <button className="btn-icon btn-danger-ghost" onClick={() => removeEntry(entry.key)} type="button">
                    <IconX size={12} />
                  </button>
                )}
              </div>

              {/* Row 2: seleção de emissoras (obrigatório se a campanha tiver emissoras) */}
              {entry.status === 'pending' && hasStations && (
                <div className="bulk-queue-stations-row">
                  <span className={`bulk-queue-stations-label${entry.stations === null ? ' required' : ''}`}>
                    Emissoras{entry.stations === null ? ' *' : ':'}
                  </span>
                  <div className="bulk-queue-stations-chips">
                    <button
                      type="button"
                      className={`station-chip${Array.isArray(entry.stations) && entry.stations.length === 0 ? ' selected' : ''}`}
                      onClick={() => setAllStations(entry.key)}
                    >
                      Todas
                    </button>
                    {campaignStations.map(s => (
                      <button
                        key={s.id}
                        type="button"
                        className={`station-chip${Array.isArray(entry.stations) && entry.stations.includes(s.id) ? ' selected' : ''}`}
                        onClick={() => toggleStation(entry.key, s.id)}
                      >
                        {Array.isArray(entry.stations) && entry.stations.includes(s.id) && <span style={{ fontSize: 10, marginRight: 2 }}>✓</span>}
                        {s.name}
                      </button>
                    ))}
                  </div>
                </div>
              )}

              {/* Resumo após upload (status done/error/uploading) */}
              {entry.status !== 'pending' && entry.stations !== null && entry.stations.length > 0 && (
                <div className="bulk-queue-stations-row">
                  <span className="bulk-queue-stations-label">Emissoras:</span>
                  <span style={{ fontSize: 12, color: 'var(--c-text-3)' }}>
                    {entry.stations.map(id => campaignStations.find(s => s.id === id)?.name).filter(Boolean).join(', ')}
                  </span>
                </div>
              )}
            </div>
          ))}

          {pendingEntries.length > 0 && (
            <div className="bulk-queue-footer">
              {!canSubmit && pendingEntries.some(e => e.stations === null) && (
                <span style={{ fontSize: 12, color: 'var(--c-danger)', marginRight: 'auto' }}>
                  Selecione as emissoras para cada material.
                </span>
              )}
              <button
                className="btn btn-primary btn-sm"
                onClick={submitAll}
                disabled={!canSubmit}
                type="button"
              >
                {uploadCommercial.isPending
                  ? <><IconSpinner /> Enviando…</>
                  : `Enviar ${pendingEntries.length} arquivo(s)`
                }
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

// ─── MaterialsPanel ────────────────────────────────────────────────────────────

function MaterialsPanel({ campaignId, campaignStationIds, allStations }) {
  const { data: commercials = [], isLoading } = useCommercials(campaignId)

  return (
    <div className="expanded-section">
      <p className="expanded-section-label">Materiais</p>

      {isLoading ? (
        <div style={{ padding: '16px 0' }}>
          {[1,2].map(i => <div key={i} className="skeleton" style={{ height: 36, borderRadius: 6, marginBottom: 6 }} />)}
        </div>
      ) : commercials.length > 0 ? (
        <table className="commercials-table">
          <thead>
            <tr>
              <th>Título</th>
              <th>Duração</th>
              <th>Fingerprint</th>
              <th>Emissoras</th>
              <th style={{ width: 80 }}></th>
            </tr>
          </thead>
          <tbody>
            {commercials.map(c => (
              <CommercialRow
                key={c.id}
                commercial={c}
                campaignStationIds={campaignStationIds}
                allStations={allStations}
              />
            ))}
          </tbody>
        </table>
      ) : null}

      <BulkUploadZone campaignId={campaignId} campaignStationIds={campaignStationIds} allStations={allStations} />
    </div>
  )
}

// ─── CampaignStationsSection ───────────────────────────────────────────────────

function CampaignStationsSection({ campaign, allStations }) {
  const updateCampaignStations = useUpdateCampaignStations()
  const [editing, setEditing]   = useState(false)

  const [stationInput, setStationInput]         = useState('')
  const [debouncedInput, setDebouncedInput]     = useState('')
  const [selectedOpts, setSelectedOpts]         = useState(() =>
    (campaign.target_stations ?? [])
      .map(id => allStations.find(s => s.id === id))
      .filter(Boolean)
      .map(s => ({ value: s.id, label: `${s.name} (${s.band}${s.frequency_mhz != null ? ` ${s.frequency_mhz}` : ''}${s.city ? ` ${s.city}` : ''})` }))
  )

  useEffect(() => {
    const t = setTimeout(() => setDebouncedInput(stationInput), 400)
    return () => clearTimeout(t)
  }, [stationInput])

  const { data: searchData, isFetching } = useStations({ q: debouncedInput || undefined, limit: 50 })
  const searchResults = searchData?.data ?? []

  const stationOptions = useMemo(() => {
    const results = searchResults.map(s => {
      const freq = s.frequency_mhz != null ? ` ${s.frequency_mhz}` : ''
      const city = s.city ? ` ${s.city}` : ''
      return { value: s.id, label: `${s.name} (${s.band}${freq}${city})` }
    })
    const resultIds = new Set(results.map(o => o.value))
    const extra = selectedOpts.filter(o => !resultIds.has(o.value))
    return [...results, ...extra]
  }, [searchResults, selectedOpts])

  function save() {
    updateCampaignStations.mutate({
      id: campaign.id,
      targetStations: selectedOpts.map(o => o.value),
    }, { onSuccess: () => setEditing(false) })
  }

  const stationsInCampaign = useMemo(
    () => (campaign.target_stations ?? []).map(id => allStations.find(s => s.id === id)).filter(Boolean),
    [campaign.target_stations, allStations]
  )

  return (
    <div className="expanded-section" style={{ borderBottom: '1px solid var(--c-border)' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 10 }}>
        <p className="expanded-section-label" style={{ marginBottom: 0 }}>Emissoras monitoradas</p>
        <button
          className="btn btn-secondary btn-sm"
          onClick={() => setEditing(v => !v)}
          type="button"
        >
          {editing ? 'Cancelar' : 'Editar'}
        </button>
      </div>

      {editing ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <RSelect
            isMulti
            options={stationOptions}
            value={selectedOpts}
            onChange={opts => setSelectedOpts(opts ?? [])}
            onInputChange={(val, { action }) => {
              if (action === 'input-change') setStationInput(val)
            }}
            filterOption={() => true}
            isLoading={isFetching}
            placeholder="Buscar emissoras…"
            closeMenuOnSelect={false}
          />
          <div style={{ display: 'flex', gap: 8 }}>
            <button
              className="btn btn-primary btn-sm"
              onClick={save}
              disabled={updateCampaignStations.isPending}
              type="button"
            >
              {updateCampaignStations.isPending ? <><IconSpinner /> Salvando</> : 'Salvar emissoras'}
            </button>
          </div>
          {campaign.status === 'ativa' && (
            <p style={{ fontSize: 12, color: 'var(--c-warning)' }}>
              Os workers serão reiniciados para aplicar as alterações.
            </p>
          )}
        </div>
      ) : stationsInCampaign.length === 0 ? (
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <p style={{ fontSize: 13, color: 'var(--c-text-3)' }}>Nenhuma emissora vinculada.</p>
          <button className="btn btn-secondary btn-sm" onClick={() => setEditing(true)} type="button">
            <IconPlus size={12} /> Adicionar
          </button>
        </div>
      ) : (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
          {stationsInCampaign.map(s => {
            const dial = stationDialLine(s)
            return (
              <div key={s.id} className="station-health-chip">
                <StationAvatar station={s} size={32} />
                <div className="station-health-chip-body">
                  <span className="station-health-chip-name">{s.name}</span>
                  {dial && <span className="station-health-chip-dial">{dial}</span>}
                </div>
                <StreamSignal station={s} />
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

// ─── CampaignRow ───────────────────────────────────────────────────────────────

function CampaignRow({ campaign, clients, allStations, cancelCampaign, deleteCampaign }) {
  const [expanded, setExpanded] = useState(false)
  const client = clients.find(cl => cl.id === campaign.client_id)
  const stationCount = (campaign.target_stations ?? []).length
  const startTip = startDateTooltip(campaign.start_date, campaign.status)
  const endTip   = endDateTooltip(campaign.end_date, campaign.status)
  const canCancel = campaign.status === 'programada' || campaign.status === 'ativa'

  async function handleCancel() {
    const ok = await window.confirm(
      `Cancelar "${campaign.name}"? Os workers param imediatamente e a campanha vai para o histórico (não é possível reativar).`
    )
    if (!ok) return
    cancelCampaign.mutate(campaign.id, {
      onError: (err) => {
        const status = err?.response?.status
        if (status === 409) {
          window.alert('Esta campanha já está em estado terminal.')
        } else {
          window.alert('Erro ao cancelar campanha.')
        }
      }
    })
  }

  return (
    <div className={`campaign-row${expanded ? ' expanded' : ''}`}>
      <div className="campaign-row-header" onClick={() => setExpanded(v => !v)}>
        <IconChevron open={expanded} />

        <StationAvatar station={{ name: client?.name ?? '?', logo_url: client?.logo_url }} size={32} />

        <div className="campaign-row-info">
          <div className="campaign-row-name">{campaign.name}</div>
          <div className="campaign-row-meta">
            {client?.name ?? '—'}
            {' · '}
            <span title={startTip}>{fmtDate(campaign.start_date)}</span>
            {' — '}
            <span title={endTip}>{fmtDate(campaign.end_date)}</span>
            {' · '}
            {stationCount} {stationCount === 1 ? 'emissora' : 'emissoras'}
          </div>
        </div>

        <div className="campaign-row-actions" onClick={e => e.stopPropagation()}>
          <span className={`badge ${STATUS_CLASS[campaign.status] ?? 'badge-concluida'}`}>
            {STATUS_LABEL[campaign.status] ?? campaign.status}
          </span>
          {canCancel && (
            <button
              className="btn btn-muted btn-sm"
              onClick={handleCancel}
              disabled={cancelCampaign.isPending}
              title="Encerrar a campanha imediatamente"
            >
              Cancelar campanha
            </button>
          )}
          <button
            className="btn btn-icon btn-danger-ghost btn-sm"
            title="Excluir campanha"
            onClick={async () => {
              if (await window.confirm(`Excluir "${campaign.name}"? Esta ação não pode ser desfeita.`)) {
                deleteCampaign.mutate(campaign.id)
              }
            }}
            disabled={deleteCampaign.isPending}
          >
            <IconTrash />
          </button>
        </div>
      </div>

      {expanded && (
        <div className="campaign-expanded">
          <CampaignStationsSection campaign={campaign} allStations={allStations} />
          <MaterialsPanel
            campaignId={campaign.id}
            campaignStationIds={campaign.target_stations ?? []}
            allStations={allStations}
          />
        </div>
      )}
    </div>
  )
}

// ─── NewCampaignModal ──────────────────────────────────────────────────────────

function NewCampaignModal({ clients, onClose }) {
  const createCampaign = useCreateCampaign()
  const emptyForm = { name: '', client_id: '', start_date: '', end_date: '', target_stations: [] }
  const [form, setForm] = useState(emptyForm)

  const [stationInput, setStationInput]   = useState('')
  const [debouncedInput, setDebouncedInput] = useState('')
  const [selectedStationOpts, setSelectedStationOpts] = useState([])

  useEffect(() => {
    const t = setTimeout(() => setDebouncedInput(stationInput), 400)
    return () => clearTimeout(t)
  }, [stationInput])

  const { data: stationSearchData, isFetching: fetchingStations } = useStations({
    q: debouncedInput || undefined,
    limit: 50,
  })
  const searchResults = stationSearchData?.data ?? []

  const stationOptions = useMemo(() => {
    const results = searchResults.map(s => {
      const freq = s.frequency_mhz != null ? ` ${s.frequency_mhz}` : ''
      const city = s.city ? ` ${s.city}` : ''
      return { value: s.id, label: `${s.name} (${s.band}${freq}${city})` }
    })
    const resultIds = new Set(results.map(o => o.value))
    const extra = selectedStationOpts.filter(o => !resultIds.has(o.value))
    return [...results, ...extra]
  }, [searchResults, selectedStationOpts])

  function setField(k, v) { setForm(f => ({ ...f, [k]: v })) }

  function handleSubmit(e) {
    e.preventDefault()
    createCampaign.mutate({
      ...form,
      start_date: form.start_date + 'T00:00:00Z',
      end_date:   form.end_date   + 'T00:00:00Z',
    }, { onSuccess: onClose })
  }

  // Close on Escape
  useEffect(() => {
    function onKey(e) { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal" style={{ maxWidth: 600 }} onClick={e => e.stopPropagation()}>
        <div className="modal-header">
          <h3>Nova campanha</h3>
          <button className="modal-close" onClick={onClose} type="button"><IconX size={14} /></button>
        </div>
        <form onSubmit={handleSubmit}>
          <div className="modal-body">
            <div className="form-row">
              <div className="field" style={{ flex: 3 }}>
                <label>Nome *</label>
                <input className="input" placeholder="ex: Campanha Verão 2025" value={form.name} onChange={e => setField('name', e.target.value)} required />
              </div>
              <div className="field" style={{ flex: 2 }}>
                <label>Cliente *</label>
                <RSelect
                  options={clients.map(c => ({ value: c.id, label: c.name }))}
                  value={clients.find(c => c.id === form.client_id) ? { value: form.client_id, label: clients.find(c => c.id === form.client_id).name } : null}
                  onChange={opt => setField('client_id', opt?.value ?? '')}
                  placeholder="Selecione…"
                  isClearable
                />
              </div>
            </div>

            <div className="form-row">
              <div className="field">
                <label>Início *</label>
                <input className="input" type="date" value={form.start_date} onChange={e => setField('start_date', e.target.value)} required />
              </div>
              <div className="field">
                <label>Fim *</label>
                <input className="input" type="date" value={form.end_date} onChange={e => setField('end_date', e.target.value)} required />
              </div>
            </div>

            <div className="field">
              <label>Emissoras (opcional — pode adicionar depois)</label>
              <RSelect
                isMulti
                options={stationOptions}
                value={selectedStationOpts}
                onChange={opts => {
                  const arr = opts ?? []
                  setSelectedStationOpts(arr)
                  setField('target_stations', arr.map(o => o.value))
                }}
                onInputChange={(val, { action }) => {
                  if (action === 'input-change') setStationInput(val)
                }}
                filterOption={() => true}
                isLoading={fetchingStations}
                placeholder="Buscar emissoras…"
                closeMenuOnSelect={false}
              />
            </div>

            {createCampaign.isError && (
              <p className="text-error">Erro ao criar campanha. Tente novamente.</p>
            )}
          </div>
          <div className="modal-footer" style={{ padding: '0 20px 20px' }}>
            <button className="btn btn-secondary btn-sm" onClick={onClose} type="button">Cancelar</button>
            <button className="btn btn-primary btn-sm" type="submit" disabled={createCampaign.isPending}>
              {createCampaign.isPending ? <><IconSpinner /> Criando…</> : 'Criar campanha'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}

// ─── EmptyState ────────────────────────────────────────────────────────────────

function EmptyState({ onNew }) {
  return (
    <div className="campaigns-empty">
      <div className="campaigns-empty-action">
        <div style={{ color: 'var(--c-action)', opacity: 0.7 }}><IconMegaphone /></div>
        <h3>Nenhuma campanha cadastrada</h3>
        <p>Crie a primeira campanha para começar a monitorar a veiculação de comerciais nas emissoras.</p>
        <button className="btn btn-primary btn-sm" onClick={onNew}>+ Nova campanha</button>
      </div>
      <div className="campaigns-empty-preview" aria-hidden="true">
        <div className="ghost-card">
          <div className="ghost-line" style={{ width: '60%' }} />
          <div className="ghost-line" style={{ width: '40%', height: 10 }} />
          <div style={{ display: 'flex', gap: 8, marginTop: 6 }}>
            <div className="ghost-line" style={{ width: 60, height: 20, borderRadius: 999 }} />
            <div className="ghost-line" style={{ width: 80, height: 20, borderRadius: 999 }} />
          </div>
        </div>
        <div className="ghost-card" style={{ opacity: 0.6 }}>
          <div className="ghost-line" style={{ width: '50%' }} />
          <div className="ghost-line" style={{ width: '35%', height: 10 }} />
        </div>
        <div className="ghost-card" style={{ opacity: 0.35 }}>
          <div className="ghost-line" style={{ width: '45%' }} />
          <div className="ghost-line" style={{ width: '30%', height: 10 }} />
        </div>
      </div>
    </div>
  )
}

// ─── CampaignsPage ─────────────────────────────────────────────────────────────

export default function CampaignsPage() {
  const { data: campaigns = [], isLoading } = useCampaigns()
  const { data: clients   = [] }            = useClients()
  const { data: allStationsData }           = useStations({ limit: 2000 })
  const allStations = allStationsData?.data ?? []

  const cancelCampaign = useCancelCampaign()
  const deleteCampaign = useDeleteCampaign()

  // Server already orders by lifecycle, but a client-side guard keeps the UX
  // consistent if the API ever changes its ORDER BY.
  const orderedCampaigns = useMemo(() => {
    return [...campaigns].sort((a, b) => {
      const ra = STATUS_ORDER[a.status] ?? 99
      const rb = STATUS_ORDER[b.status] ?? 99
      if (ra !== rb) return ra - rb
      // Within same status: programada → soonest first; everything else → most recent first.
      if (a.status === 'programada') {
        return new Date(a.start_date) - new Date(b.start_date)
      }
      return new Date(b.start_date) - new Date(a.start_date)
    })
  }, [campaigns])

  const [showModal, setShowModal] = useState(false)

  if (isLoading) {
    return (
      <div>
        <div className="page-header">
          <h2>Campanhas</h2>
        </div>
        {[1,2,3].map(i => (
          <div key={i} className="campaign-row" style={{ marginBottom: 8 }}>
            <div className="campaign-row-header" style={{ pointerEvents: 'none' }}>
              <div className="skeleton" style={{ width: 14, height: 14, borderRadius: 3 }} />
              <div className="skeleton" style={{ width: 32, height: 32, borderRadius: 8 }} />
              <div style={{ flex: 1 }}>
                <div className="skeleton" style={{ width: '35%', height: 14, borderRadius: 4, marginBottom: 6 }} />
                <div className="skeleton" style={{ width: '55%', height: 11, borderRadius: 4 }} />
              </div>
              <div className="skeleton" style={{ width: 60, height: 22, borderRadius: 999 }} />
            </div>
          </div>
        ))}
      </div>
    )
  }

  return (
    <div>
      <div className="page-header">
        <h2>Campanhas</h2>
        <button className="btn btn-primary btn-sm" onClick={() => setShowModal(true)}>
          + Nova campanha
        </button>
      </div>

      {campaigns.length === 0 ? (
        <EmptyState onNew={() => setShowModal(true)} />
      ) : (
        <div className="campaign-list">
          {orderedCampaigns.map(c => (
            <CampaignRow
              key={c.id}
              campaign={c}
              clients={clients}
              allStations={allStations}
              cancelCampaign={cancelCampaign}
              deleteCampaign={deleteCampaign}
            />
          ))}
        </div>
      )}

      {showModal && (
        <NewCampaignModal
          clients={clients}
          onClose={() => setShowModal(false)}
        />
      )}
    </div>
  )
}
