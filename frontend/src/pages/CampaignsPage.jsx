import { useState, useRef, useEffect, useMemo } from 'react'
import { createPortal } from 'react-dom'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import {
  useCampaignsPaged, useCancelCampaign,
  useClients, useStations, useCommercials, useUploadCommercial,
  useUpdateCommercialStations, useUpdateCampaignStations, useDeleteCommercial,
  useCampaignMaterials, useMaterials, useCampaignsFinancials,
} from '../api/hooks'
import api from '../api/client'
import RSelect from '../components/RSelect'
import StationAvatar from '../components/StationAvatar'
import AirtimePaginator from '../components/AirtimePaginator'
import { useConfirm, useAlert } from '../components/ConfirmModal'
import CampaignReportsMenu from '../components/CampaignReportsMenu'
import { useAuth } from '../contexts/AuthContext'

const CAMPAIGNS_PAGE_SIZE = 12

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
// Lifecycle ordering used to live here as STATUS_ORDER for client-side sort,
// but server now owns the order — kept the comment for code archaeology.
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

function IconPencil() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
      <path d="M9.5 2L12 4.5L4.5 12H2V9.5L9.5 2Z" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round"/>
      <path d="M8.5 3L11 5.5" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round"/>
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
  const [audioBlobUrl, setAudioBlobUrl] = useState(null)
  const [audioLoading, setAudioLoading] = useState(false)
  const [downloadLoading, setDownloadLoading] = useState(false)
  const btnRef = useRef(null)
  const audioRef = useRef(null)
  const deleteCommercial = useDeleteCommercial()

  // Revoke blob URL on unmount to free memory.
  useEffect(() => {
    return () => { if (audioBlobUrl) URL.revokeObjectURL(audioBlobUrl) }
  }, [audioBlobUrl])

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

  // fetchAudioBlob fetches the master audio with JWT auth and returns a blob URL.
  // The <audio> element cannot send Authorization headers directly, so we go
  // through the axios client (which injects the token) and create an object URL.
  async function fetchAudioBlob() {
    if (audioBlobUrl) return audioBlobUrl
    const resp = await api.get(`/commercials/${commercial.id}/audio`, { responseType: 'blob' })
    const url = URL.createObjectURL(resp.data)
    setAudioBlobUrl(url)
    return url
  }

  async function togglePlay() {
    if (playing) { setPlaying(false); return }
    if (audioLoading) return
    setAudioLoading(true)
    try {
      await fetchAudioBlob()
      setPlaying(true)
    } catch {
      window.alert('Não foi possível carregar o áudio.')
    } finally {
      setAudioLoading(false)
    }
  }

  async function handleDownload() {
    if (downloadLoading) return
    setDownloadLoading(true)
    try {
      const url = await fetchAudioBlob()
      const ext = commercial.master_storage_path?.split('.').pop() ?? 'mp3'
      const a = document.createElement('a')
      a.href = url
      a.download = `${commercial.title}.${ext}`
      document.body.appendChild(a)
      a.click()
      a.remove()
    } catch {
      window.alert('Não foi possível baixar o áudio.')
    } finally {
      setDownloadLoading(false)
    }
  }

  useEffect(() => {
    if (playing && audioRef.current) {
      audioRef.current.play().catch(() => setPlaying(false))
    } else if (!playing && audioRef.current) {
      audioRef.current.pause()
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
              disabled={audioLoading}
              style={{ color: playing ? 'var(--c-action)' : 'var(--c-text-2)' }}
            >
              {playing ? <IconStop /> : <IconPlay />}
            </button>
            <button
              className="btn btn-icon btn-sm"
              onClick={handleDownload}
              type="button"
              title="Baixar"
              disabled={downloadLoading}
              style={{ color: 'var(--c-text-2)' }}
            >
              <IconDownload />
            </button>
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
              src={audioBlobUrl ?? ''}
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
        // entry.stations:
        //   null  → estado inválido (botão Enviar fica desabilitado)
        //   []    → "Todas" (UI). Backend trata target_stations=[] como INATIVO,
        //           então expandimos aqui para o conjunto atual de emissoras da
        //           campanha. Sem essa expansão o material subia inativo e
        //           ninguém percebia (incidente 2026-05-08).
        //   [ids] → específicas
        const picked = entry.stations ?? []
        const targetStations = (picked.length === 0 && hasStations)
          ? campaignStations.map(s => s.id)
          : picked
        if (targetStations.length > 0) {
          await new Promise((resolve, reject) => {
            updateStations.mutate(
              { id: created.id, campaignId, targetStations },
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

function MaterialsPanel({ campaign, campaignStationIds, allStations }) {
  const campaignId = campaign.id
  const clientId = campaign.client_id
  const { data: commercials = [], isLoading: loadingCom } = useCommercials(campaignId)
  const { data: campaignMaterials = [], isLoading: loadingCM } = useCampaignMaterials(campaignId)
  const { data: clientLibrary = [] } = useMaterials(clientId)
  const isLoading = loadingCom || loadingCM

  const libraryById = useMemo(
    () => Object.fromEntries(clientLibrary.map(m => [m.id, m])),
    [clientLibrary]
  )

  // Merge: prefer items from campaign_materials (new schema) when both exist.
  // After Plan 1 backfill, commercial.id == material.id for migrated rows.
  const items = useMemo(() => {
    const fromLinks = campaignMaterials
      .map(link => {
        const mat = libraryById[link.material_id]
        if (!mat) return null
        // Shape it like a Commercial so CommercialRow can render it unchanged.
        return {
          id: mat.id,
          short_id: mat.short_id,
          campaign_id: campaignId,
          title: mat.title,
          cut_label: null,
          duration_seconds: mat.duration_seconds,
          master_storage_path: mat.master_storage_path,
          master_sha256: mat.master_sha256,
          fingerprint_status: mat.fingerprint_status,
          fingerprint_generated_at: mat.fingerprint_generated_at,
          fingerprint_hash_count: mat.fingerprint_hash_count,
          target_stations: link.target_stations,
          created_at: link.added_at,
        }
      })
      .filter(Boolean)

    const linkedIds = new Set(fromLinks.map(x => x.id))
    const fromOld = commercials.filter(c => !linkedIds.has(c.id))
    return [...fromLinks, ...fromOld]
  }, [campaignMaterials, libraryById, commercials, campaignId])

  return (
    <div className="expanded-section">
      <p className="expanded-section-label">Materiais</p>

      {isLoading ? (
        <div style={{ padding: '16px 0' }}>
          {[1,2].map(i => <div key={i} className="skeleton" style={{ height: 36, borderRadius: 6, marginBottom: 6 }} />)}
        </div>
      ) : items.length > 0 ? (
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
            {items.map(c => (
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
  const { isAdmin } = useAuth()
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
        {isAdmin && (
          <button
            className="btn btn-secondary btn-sm"
            onClick={() => setEditing(v => !v)}
            type="button"
          >
            {editing ? 'Cancelar' : 'Editar'}
          </button>
        )}
      </div>

      {isAdmin && editing ? (
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
          {isAdmin && (
            <button className="btn btn-secondary btn-sm" onClick={() => setEditing(true)} type="button">
              <IconPlus size={12} /> Adicionar
            </button>
          )}
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

// Bloco financeiro da row: CPM (protagonista, em Rosa Digital — o número que
// o operador compara entre campanhas) com o investimento total logo abaixo,
// como apoio, num slot próprio à direita do bloco de info, antes do cluster
// de ações. O rosa no valor é sancionado pelo DESIGN.md ("Charts de Custo"
// usam tertiary) e, sem fundo, lê como número e não como botão. Números
// tabulares + largura mínima fixa fazem o skeleton e os valores carregados
// ocuparem a mesma caixa: os números surgem no lugar, sem o "pop" que a
// linha-meta tinha (financials vem de uma query separada da lista, então
// durante o fetch o slot fica reservado pelo skeleton).
const _BRL_CAMPAIGN_LIST = new Intl.NumberFormat('pt-BR', {
  style: 'currency', currency: 'BRL', minimumFractionDigits: 2, maximumFractionDigits: 2,
})
// targetLabel: rótulo do público-alvo do cliente (clients.target_label). Entra
// SÓ nos tooltips de "CPM no target"/"Impactos no target" — os rótulos visíveis
// são curtos por restrição de espaço na row. null = sem rótulo → tooltips
// idênticos aos de antes.
function CampaignFinancials({ financials, loading, targetLabel = null }) {
  // Enquanto a query de financials não resolveu, reserva o espaço com o
  // esqueleto de mesma forma — evita layout shift quando o valor entra.
  if (loading) return <CampaignFinancialsSkeleton />

  const {
    total_invested: inv, total_bonus_value: bonusValue = 0,
    total_insertions: ins, total_audience: aud,
    total_audience_target: audTarget, stations_with_target: withTarget,
    fixed_cpm: fixed,
  } = financials ?? {}
  // NUMERADOR DO CPM = investido + bonificado (definição do dono, 2026-08-17).
  // "Investido" é só o que o cliente pagou (unit × in_slot); o bônus é entrega
  // gratuita e vive em total_bonus_value. Mas o CPM mede a eficiência da MÍDIA
  // ENTREGUE A PREÇO DE TABELA, não a da negociação: a tocada de bônus foi ao ar
  // e já está no denominador (total_audience conta in_slot + bonus), então tem
  // que estar no numerador ao preço de tabela dela. Só com o pago, campanha com
  // muito bônus exibiria um CPM artificialmente baixo, incomparável com o das
  // outras. Não "simplifique" de volta pra `inv / aud`.
  const entregue = (inv ?? 0) + (bonusValue ?? 0)
  // Sem pricing cadastrado: nada a mostrar (o slot colapsa após o load). O gate
  // é o numerador inteiro — campanha 100% bonificada tem invested 0 e mesmo
  // assim tem CPM.
  if (!financials || !(entregue > 0)) return null

  // CPM fixo (quando setado na campanha) sobrescreve o cálculo dinâmico, pra
  // refletir o número comercial pré-acordado em vez do derivado de pricing.
  const dynamicCPM = aud > 0 ? (entregue / aud) * 1000 : null
  const cpm = fixed != null ? fixed : dynamicCPM
  const isFixed = fixed != null
  // CPM no target é SEMPRE dinâmico — o CPM fixo é contratado sobre a audiência
  // total, não sobre o recorte de público-alvo. Mesmo numerador do CPM cheio.
  const hasTarget = (withTarget ?? 0) > 0 && audTarget > 0
  const cpmTarget = hasTarget ? (entregue / audTarget) * 1000 : null
  const targetSuffix = targetLabel ? ` (${targetLabel})` : ''
  const entregueTip = bonusValue > 0
    ? `${_BRL_CAMPAIGN_LIST.format(entregue)} entregues a preço de tabela (${_BRL_CAMPAIGN_LIST.format(inv)} investidos + ${_BRL_CAMPAIGN_LIST.format(bonusValue)} de bonificação)`
    : _BRL_CAMPAIGN_LIST.format(entregue)
  const cpmTip = isFixed
    ? `CPM fixo da campanha: ${_BRL_CAMPAIGN_LIST.format(fixed)}. ${entregueTip} sobre ${ins} inserções (CPM dinâmico seria ${dynamicCPM != null ? _BRL_CAMPAIGN_LIST.format(dynamicCPM) : '—'}).`
    : cpm != null
      ? `${entregueTip} ÷ (${ins} inserções × PMM = ${Math.round(aud).toLocaleString('pt-BR')} impressões) × 1000`
      : ins > 0
        ? 'Emissoras sem PMM cadastrado — CPM indeterminado.'
        : 'Nenhuma inserção realizada ainda — CPM indeterminado.'

  return (
    <div className="campaign-fin">
      <span className="campaign-fin-label">CPM</span>
      <span
        className={`campaign-fin-value${cpm == null ? ' campaign-fin-value--empty' : ''}`}
        title={cpmTip}
      >
        {cpm != null ? _BRL_CAMPAIGN_LIST.format(cpm) : '—'}
        {isFixed && <span className="campaign-fin-tag">fixo</span>}
      </span>
      {hasTarget && (
        <span className="campaign-fin-sub"
              title={`CPM no target${targetSuffix}: ${_BRL_CAMPAIGN_LIST.format(cpmTarget)}, sempre dinâmico ((investido + bonificado) ÷ impactos no target × 1000).`}>
          <span className="campaign-fin-sub-label">CPM no target</span> {_BRL_CAMPAIGN_LIST.format(cpmTarget)}
        </span>
      )}
      {/* aud === 0 não é "zero impactos" — é indeterminado (mesma causa que
          deixa o CPM em "—" ao lado: sem PMM cadastrado ou sem inserção
          ainda). Mostrar "0" seria factualmente errado. */}
      <span className="campaign-fin-sub" title={aud > 0
        ? `${Math.round(aud).toLocaleString('pt-BR')} impressões`
        : ins > 0
          ? 'Emissoras sem PMM cadastrado — impactos indeterminados.'
          : 'Nenhuma inserção realizada ainda — impactos indeterminados.'}>
        <span className="campaign-fin-sub-label">Impactos</span> {aud > 0 ? Math.round(aud).toLocaleString('pt-BR') : '—'}
      </span>
      {hasTarget && (
        <span className="campaign-fin-sub"
              title={`${Math.round(audTarget).toLocaleString('pt-BR')} impactos no target${targetSuffix} · ${withTarget} emissoras com target cadastrado`}>
          <span className="campaign-fin-sub-label">Impactos no target</span> {Math.round(audTarget).toLocaleString('pt-BR')}
        </span>
      )}
      <span className="campaign-fin-sub" title={`Investimento total: ${_BRL_CAMPAIGN_LIST.format(inv)}`}>
        <span className="campaign-fin-sub-label">Investimento</span> {_BRL_CAMPAIGN_LIST.format(inv)}
      </span>
    </div>
  )
}

// Placeholder de mesma pegada visual do bloco financeiro carregado. Cobre o
// caso comum (sem target cadastrado: rótulo / valor / Impactos / Investimento
// — 4 barras) em vez do teto de 6 linhas que o bloco pode chegar a ter com
// target cadastrado; a maioria das campanhas ainda não tem target, então
// dimensionar pro teto reservaria espaço demais no caso comum e o "shift pra
// baixo" apareceria de qualquer forma quando o dado real (mais compacto)
// chegasse. Evita o pop pra quem não usa a feature; quem usa ainda vê um
// pequeno crescimento da caixa ao carregar — aceitável, não é o caso comum.
function CampaignFinancialsSkeleton() {
  return (
    <div className="campaign-fin campaign-fin--skeleton" aria-hidden="true">
      <div className="skeleton" style={{ width: 58, height: 8, borderRadius: 3 }} />
      <div className="skeleton" style={{ width: 88, height: 15, borderRadius: 4 }} />
      <div className="skeleton" style={{ width: 70, height: 10, borderRadius: 3 }} />
      <div className="skeleton" style={{ width: 60, height: 10, borderRadius: 3 }} />
    </div>
  )
}

function CampaignRow({ campaign, clients, allStations, cancelCampaign, financials, financialsLoading }) {
  const confirm = useConfirm()
  const alertDialog = useAlert()
  const navigate = useNavigate()
  const { isAdmin } = useAuth()
  const client = clients.find(cl => cl.id === campaign.client_id)
  const stationCount = (campaign.target_stations ?? []).length
  const startTip = startDateTooltip(campaign.start_date, campaign.status)
  const endTip   = endDateTooltip(campaign.end_date, campaign.status)
  const canCancel = campaign.status === 'programada' || campaign.status === 'ativa'

  // Whole-row navigation: clicking anywhere on the listing card opens the
  // campaign's airings at /detections?campaign_id={id} (the campaign comes
  // pre-selected via the deep-link param). Editing is NOT triggered by the
  // card click anymore — it lives only on the explicit pencil button in the
  // right cluster. Action buttons there stop propagation to keep their own
  // semantics. We also honor cmd/ctrl+click for new-tab.
  const detectionsHref = `/detections?campaign_id=${campaign.id}`
  function handleRowClick(e) {
    // Ignore if the click landed on an interactive descendant we don't own
    // (button / link / etc.) — those handle themselves.
    if (e.target.closest('button, a, input, select, [role="button"]')) return
    if (e.metaKey || e.ctrlKey) {
      window.open(detectionsHref, '_blank', 'noopener')
      return
    }
    navigate(detectionsHref)
  }

  async function handleCancel() {
    const ok = await confirm(
      `Cancelar "${campaign.name}"? Os workers param imediatamente e a campanha vai para o histórico (não é possível reativar).`
    )
    if (!ok) return
    cancelCampaign.mutate(campaign.id, {
      onError: (err) => {
        const status = err?.response?.status
        if (status === 409) {
          alertDialog('Esta campanha já está em estado terminal.')
        } else {
          alertDialog('Erro ao cancelar campanha.')
        }
      }
    })
  }

  return (
    <div
      className="campaign-row campaign-row--clickable"
      onClick={handleRowClick}
      role="link"
      tabIndex={0}
      onKeyDown={e => {
        if (e.key === 'Enter' || e.key === ' ') {
          if (e.target.closest('button, a, input, select')) return
          e.preventDefault()
          navigate(detectionsHref)
        }
      }}
      title="Ver veiculação da campanha"
    >
      <div className="campaign-row-header">
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

        {/* targetLabel sai do cliente da campanha (já resolvido acima em
            `client`). Vazio/ausente → null e os tooltips ficam como antes. */}
        <CampaignFinancials
          financials={financials}
          loading={financialsLoading}
          targetLabel={(client?.target_label ?? '').trim() || null}
        />

        <div className="campaign-row-actions">
          {/* Relatórios: CSV consolidado / detalhado / PDF. Sem from/to →
              o backend considera a campanha inteira. Stops row click. */}
          <span onClick={e => e.stopPropagation()}>
            <CampaignReportsMenu
              campaignId={campaign.id}
              variant="compact"
              placement="bottom-end"
              label="Relatórios"
            />
          </span>
          <span className={`badge ${STATUS_CLASS[campaign.status] ?? 'badge-concluida'}`}>
            {STATUS_LABEL[campaign.status] ?? campaign.status}
          </span>
          {campaign.material_count === 0 && (
            <span
              title="Esta campanha ainda não tem nenhum material vinculado — abra o wizard pra subir o áudio quando estiver pronto."
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: 5,
                padding: '2px 8px',
                borderRadius: 'var(--radius-full)',
                background: '#fef9c3',
                color: '#a16207',
                fontSize: 10,
                fontWeight: 700,
                letterSpacing: '0.03em',
                fontFamily: 'var(--font-heading)',
              }}
            >
              <svg width="9" height="9" viewBox="0 0 16 16" fill="none"
                   stroke="currentColor" strokeWidth="2" strokeLinecap="round"
                   strokeLinejoin="round">
                <path d="M8 1.5L1.5 13.5h13L8 1.5z" />
                <path d="M8 6v3.5M8 11.5v.5" />
              </svg>
              sem material
            </span>
          )}
          {isAdmin && (
            <Link
              to={`/campaigns/${campaign.id}/edit`}
              className="btn btn-icon btn-sm"
              title="Editar campanha"
              onClick={e => e.stopPropagation()}
              style={{ color: 'var(--c-text-2)' }}
            >
              <IconPencil />
            </Link>
          )}
          {isAdmin && canCancel && (
            <button
              className="btn btn-icon btn-danger-ghost btn-sm"
              onClick={handleCancel}
              disabled={cancelCampaign.isPending}
              title="Cancelar campanha (vai para o histórico)"
            >
              <IconTrash />
            </button>
          )}
        </div>
      </div>

    </div>
  )
}

// ─── EmptyState ────────────────────────────────────────────────────────────────

function EmptyState() {
  const { isAdmin } = useAuth()
  return (
    <div className="campaigns-empty">
      <div className="campaigns-empty-action">
        <div style={{ color: 'var(--c-action)', opacity: 0.7 }}><IconMegaphone /></div>
        <h3>Nenhuma campanha cadastrada</h3>
        <p>Crie a primeira campanha para começar a monitorar a veiculação de comerciais nas emissoras.</p>
        {isAdmin && <Link to="/campaigns/new" className="btn btn-primary btn-sm">+ Nova campanha</Link>}
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
  const { isAdmin } = useAuth()
  // /clients devolve scope-aware: admin vê a lista inteira, viewer recebe
  // apenas o próprio cliente — suficiente pro find(...) por client_id no
  // CampaignRow resolver nome+logo nas duas roles.
  const { data: clients   = [] }            = useClients()
  const { data: allStationsData }           = useStations({ limit: 2000 })
  const allStations = allStationsData?.data ?? []
  const cancelCampaign = useCancelCampaign()

  // Two-tier search state:
  //   searchInput → what's in the textbox (re-renders only the input itself)
  //   search      → the debounced value handed to the paged query
  // This decoupling keeps the input focused while the user types: only the
  // input re-renders on each keystroke, and the query fires 300ms after the
  // last change.
  const [searchInput, setSearchInput] = useState('')
  const [search, setSearch] = useState('')
  // Competência default = mês atual. O usuário pode limpar pra ver todas, mas
  // o caso comum é "quais campanhas estão ativas neste mês". Selecionar um
  // mês mantém só campanhas cujo intervalo [start_date, end_date] o cruza.
  const [competence, setCompetence] = useState(() => {
    const now = new Date()
    return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
  })
  const [page, setPage] = useState(1)

  // Debounce searchInput → search (300ms). Re-armed on every keystroke;
  // cleanup runs on unmount so a pending fire-after-unmount can't leak.
  const debounceRef = useRef(null)
  function changeSearch(v) {
    setSearchInput(v)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => {
      setSearch(v)
      setPage(1)
    }, 300)
  }
  useEffect(() => () => {
    if (debounceRef.current) clearTimeout(debounceRef.current)
  }, [])

  function changeCompetence(v) { setCompetence(v); setPage(1) }
  function clearFilters() {
    if (debounceRef.current) clearTimeout(debounceRef.current)
    setSearchInput('')
    setSearch('')
    setCompetence('')
    setPage(1)
  }

  // Admin deep-link: /admin/station-failures envia ?campaign=<uuid> pra abrir
  // uma campanha específica. Quando setado, faz o backend filtrar pelo id
  // (ignora q/competence) e a UI mostra um banner sticky com nome + botão ×.
  const [searchParams, setSearchParams] = useSearchParams()
  const campaignFilterId = searchParams.get('campaign') || ''

  // Server-side pagination: backend handles filter + sort + paging, returns
  // {data, total, total_pages, page, page_size}. Stale data stays visible
  // during navigation thanks to keepPreviousData on the hook.
  const { data: pagedResp, isLoading, isFetching } = useCampaignsPaged({
    q: search,
    competence,
    id: campaignFilterId,
    page,
    pageSize: CAMPAIGNS_PAGE_SIZE,
  })
  const pageCampaigns = pagedResp?.data ?? []
  const totalFiltered = pagedResp?.total ?? 0
  const totalPages    = pagedResp?.total_pages ?? 1

  // CPM só das 12 campanhas visíveis. O agregado varre daily_play_summary,
  // que é o passo caro da rota — pedir a base inteira pra pintar uma página
  // fazia o cliente esperar o mesmo que o admin. O join no id abaixo é o
  // recorte; a carteira do JWT continua valendo no backend.
  const pageCampaignIds = useMemo(() => pageCampaigns.map(c => c.id), [pageCampaigns])
  const { data: financialsList = [], isPending: financialsLoading } =
    useCampaignsFinancials(pageCampaignIds)
  const financialsByCampaign = useMemo(
    () => Object.fromEntries(financialsList.map(f => [f.campaign_id, f])),
    [financialsList]
  )

  const filteredCampaignName = campaignFilterId
    ? (pageCampaigns[0]?.name || '')
    : ''

  const hasFilters = !!search || !!competence
  const safePage   = Math.min(page, Math.max(1, totalPages))

  // total === 0 + no filters = catalog is empty (first-run state).
  // total === 0 + filters    = nothing matched the user's narrowing.
  // Both are only meaningful AFTER the first fetch — otherwise we'd flash
  // "no campaigns" before the catalog finishes loading.
  const initialEmpty  = !hasFilters && totalFiltered === 0 && !isLoading
  const filteredEmpty = hasFilters && totalFiltered === 0 && !isLoading

  return (
    <div>
      <div className="page-header">
        <h2>Campanhas</h2>
        {isAdmin && <Link to="/campaigns/new" className="btn btn-primary btn-sm">+ Nova campanha</Link>}
      </div>

      {campaignFilterId && (
        <div className="campaigns-filter-banner">
          <span>
            Filtrado: <strong>{filteredCampaignName || 'campanha específica'}</strong>
          </span>
          <button
            type="button"
            className="campaigns-filter-clear"
            onClick={() => { setSearchParams({}) }}
            aria-label="Limpar filtro"
          >
            ×
          </button>
        </div>
      )}

      {initialEmpty ? (
        <EmptyState />
      ) : (
        <>
          <CampaignFilters
            search={searchInput}
            onSearchChange={changeSearch}
            competence={competence}
            onCompetenceChange={changeCompetence}
            onClear={clearFilters}
            hasFilters={hasFilters}
            total={totalFiltered}
            shown={totalFiltered}
          />

          {isLoading ? (
            <CampaignListSkeleton />
          ) : filteredEmpty ? (
            <FilteredEmptyState onClear={clearFilters} />
          ) : (
            <>
              <div className="campaign-list" style={{ opacity: isFetching ? 0.7 : 1, transition: 'opacity 150ms' }}>
                {pageCampaigns.map(c => (
                  <CampaignRow
                    key={c.id}
                    campaign={c}
                    clients={clients}
                    allStations={allStations}
                    cancelCampaign={cancelCampaign}
                    financials={financialsByCampaign[c.id]}
                    financialsLoading={financialsLoading}
                  />
                ))}
              </div>
              <AirtimePaginator
                page={safePage}
                totalPages={totalPages}
                total={totalFiltered}
                pageSize={CAMPAIGNS_PAGE_SIZE}
                onChange={setPage}
                singular="campanha"
                plural="campanhas"
              />
            </>
          )}
        </>
      )}
    </div>
  )
}

// Filter bar above the campaign list — text search (name / client) plus a
// competence picker that uses month-overlap semantics: a campaign appears for
// any month its [start_date, end_date] interval touches.
// Filter bar above the campaign list — competência (single step from the
// shared .flow-filter family) plus an accent-insensitive token search on
// name/client. Matches the visual language used in /detections + /airtime.
function CampaignFilters({
  search, onSearchChange, competence, onCompetenceChange,
  onClear, hasFilters, total, shown,
}) {
  return (
    <div style={{ marginBottom: 16 }}>
      <div style={{
        display: 'flex', alignItems: 'stretch', gap: 14, flexWrap: 'wrap',
      }}>
        <div className="flow-filter flow-filter--active" style={{ flex: '0 0 220px', maxWidth: 240 }}>
          <label className="flow-filter-label" htmlFor="campaigns-competence">
            <span className="flow-filter-label-step">1</span>
            Competência
          </label>
          <input
            id="campaigns-competence"
            className="flow-month-input"
            type="month"
            value={competence}
            onChange={e => onCompetenceChange(e.target.value)}
          />
        </div>

        <div className="flow-filter" style={{ flex: '1 1 280px', minWidth: 240, maxWidth: 460 }}>
          <label className="flow-filter-label" htmlFor="campaigns-search">
            Buscar
            {hasFilters && (
              <span style={{
                marginLeft: 'auto',
                textTransform: 'none', letterSpacing: 0,
                fontSize: 11, fontWeight: 600, color: 'var(--c-text-3)',
              }}>
                {shown === 1 ? '1 campanha' : `${shown} campanhas`}
              </span>
            )}
          </label>
          <div className="stations-search" style={{ width: '100%', maxWidth: 'none' }}>
            <span className="stations-search-icon">
              <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75">
                <circle cx="7" cy="7" r="5" /><path d="M11 11l3 3" strokeLinecap="round" />
              </svg>
            </span>
            <input
              id="campaigns-search"
              className="input stations-search-input"
              type="text"
              placeholder="Buscar por nome ou cliente…"
              value={search}
              onChange={e => onSearchChange(e.target.value)}
            />
          </div>
        </div>

        {hasFilters && (
          <div style={{ display: 'flex', alignItems: 'flex-end' }}>
            <button
              type="button"
              onClick={onClear}
              style={{
                height: 38, padding: '0 14px', borderRadius: 'var(--radius-md)',
                background: 'var(--c-surface)', border: '1px solid var(--c-border)',
                color: 'var(--c-text-2)', fontSize: 12, fontWeight: 600,
                cursor: 'pointer', fontFamily: 'var(--font-body)',
                transition: 'all 150ms',
              }}
              onMouseEnter={e => {
                e.currentTarget.style.borderColor = 'var(--c-action-border)'
                e.currentTarget.style.color = 'var(--c-action)'
              }}
              onMouseLeave={e => {
                e.currentTarget.style.borderColor = 'var(--c-border)'
                e.currentTarget.style.color = 'var(--c-text-2)'
              }}
            >
              Limpar
            </button>
          </div>
        )}
      </div>
    </div>
  )
}

// Row-shaped skeletons that occupy the same vertical space as a CampaignRow.
// Scoped to the list so the filter bar stays mounted (and focused) across
// query refetches.
function CampaignListSkeleton() {
  return (
    <div className="campaign-list">
      {[1, 2, 3, 4, 5].map(i => (
        <div key={i} className="campaign-row" style={{ marginBottom: 8 }}>
          <div className="campaign-row-header" style={{ pointerEvents: 'none' }}>
            <div className="skeleton" style={{ width: 14, height: 14, borderRadius: 3 }} />
            <div className="skeleton" style={{ width: 32, height: 32, borderRadius: 8 }} />
            <div style={{ flex: 1 }}>
              <div className="skeleton" style={{ width: '35%', height: 14, borderRadius: 4, marginBottom: 6 }} />
              <div className="skeleton" style={{ width: '55%', height: 11, borderRadius: 4 }} />
            </div>
            <CampaignFinancialsSkeleton />
            <div className="skeleton" style={{ width: 60, height: 22, borderRadius: 999 }} />
          </div>
        </div>
      ))}
    </div>
  )
}

function FilteredEmptyState({ onClear }) {
  return (
    <div style={{
      display: 'flex', flexDirection: 'column', alignItems: 'center',
      gap: 14, padding: '64px 32px', textAlign: 'center',
      background: 'var(--c-surface)', border: '1px solid var(--c-border)',
      borderRadius: 'var(--radius-xl)',
    }}>
      <div style={{
        width: 56, height: 56, borderRadius: 'var(--radius-lg)',
        background: 'var(--c-surface-2)', color: 'var(--c-text-2)',
        display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
        boxShadow: '0 0 0 6px rgba(100, 116, 139, 0.04)',
      }}>
        <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <circle cx="11" cy="11" r="7" />
          <path d="m21 21-4.3-4.3" />
        </svg>
      </div>
      <h3 style={{
        fontFamily: 'var(--font-heading)', fontWeight: 700, fontSize: 20,
        color: 'var(--c-text)', margin: 0, letterSpacing: '-0.01em',
      }}>Nenhuma campanha encontrada</h3>
      <p style={{
        margin: 0, color: 'var(--c-text-2)', fontSize: 14, lineHeight: 1.5,
        maxWidth: 380,
      }}>
        Nenhuma campanha corresponde aos filtros aplicados. Tente outra competência ou limpe os filtros pra ver todas.
      </p>
      <button
        type="button"
        onClick={onClear}
        style={{
          marginTop: 4,
          padding: '10px 18px', borderRadius: 'var(--radius-md)',
          background: 'var(--c-action)', color: '#fff',
          border: '1px solid var(--c-action)',
          fontSize: 13, fontWeight: 600, cursor: 'pointer',
          fontFamily: 'var(--font-body)',
          transition: 'all 150ms cubic-bezier(0.16, 1, 0.3, 1)',
        }}
      >
        Limpar filtros
      </button>
    </div>
  )
}
