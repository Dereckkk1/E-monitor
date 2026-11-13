// AdminRecordingsPage.jsx — /admin/gravacoes.
//
// Programa a gravação de um trecho de stream e lista o que já foi gravado. O
// áudio fica 15 dias e depois é apagado. Ver docs/features/scheduled-recording.md.
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'

import {
  fetchRecordingAudio, useCancelRecording, useCreateRecording, useDeleteRecording,
  useRecordings, useStationConnectionTest,
} from '../api/hooks'
import AudioPlayer from '../components/AudioPlayer'
import { useAlert, useConfirm } from '../components/ConfirmModal'
import FlowEmptyState from '../components/FlowEmptyState'
import StationAvatar from '../components/StationAvatar'
import StationSearch from '../components/StationSearch'
import {
  coveragePercent, downloadName, errorMessage, expiresLabel, gapLabels, recordingStation, stationSub, statusInfo,
  todayInSaoPaulo, windowLabel,
} from '../utils/recordings'
import './AdminRecordingsPage.css'

// Ícones no traço dos de /campaigns (14px, viewBox 14).
function IconPlay() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
      <path d="M3.5 2.5V11.5L11 7L3.5 2.5Z" fill="currentColor" />
    </svg>
  )
}
function IconPause() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
      <rect x="3.5" y="2.5" width="2.4" height="9" rx="0.6" fill="currentColor" />
      <rect x="8.1" y="2.5" width="2.4" height="9" rx="0.6" fill="currentColor" />
    </svg>
  )
}
function IconDownload() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
      <path d="M7 2V9M7 9L4 6M7 9L10 6" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M2.5 11H11.5" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
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
function IconX() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
      <path d="M3.5 3.5L10.5 10.5M10.5 3.5L3.5 10.5" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  )
}
function IconTrash() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
      <path d="M2 3.5H12M5.5 3.5V2.5C5.5 2.22 5.72 2 6 2H8C8.28 2 8.5 2.22 8.5 2.5V3.5M5.5 6.5V10.5M8.5 6.5V10.5M3 3.5L3.5 11.5C3.5 11.78 3.72 12 4 12H10C10.28 12 10.5 11.78 10.5 11.5L11 3.5" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}
function IconPencil() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
      <path d="M9.5 2.5L11.5 4.5L5 11L2.5 11.5L3 9L9.5 2.5Z" stroke="currentColor" strokeWidth="1.3" strokeLinejoin="round" />
      <path d="M8.5 3.5L10.5 5.5" stroke="currentColor" strokeWidth="1.3" />
    </svg>
  )
}
function IconCheck() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
      <path d="M2.5 7.5L5.5 10.5L11.5 3.5" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

// A emissora como aparece no seletor: logo, nome e "dial · cidade/UF".
function StationIdentity({ station, size = 32, className = '' }) {
  return (
    <div className={`ar-identity ${className}`}>
      <StationAvatar station={station} size={size} />
      <div className="ar-identity-body">
        <span className="ar-identity-name">{station.name}</span>
        {station.sub && <span className="ar-identity-sub">{station.sub}</span>}
      </div>
    </div>
  )
}

// O que o seletor devolveu, no formato do StationIdentity.
function pickedStation(selection) {
  const st = selection.station
  return { name: st.name, logo_url: st.logo_url ?? null, sub: stationSub(st) }
}

// Os quatro campos são obrigatórios e nenhum destrava o outro, então não há
// --locked: enquanto vazio o passo fica --active ("falta"), preenchido vira --done.
function stepClass(filled) {
  return `flow-filter ${filled ? 'flow-filter--done' : 'flow-filter--active'}`
}

function RecordingForm() {
  const [selection, setSelection] = useState(null)
  const [day, setDay] = useState(() => todayInSaoPaulo())
  const [start, setStart] = useState('')
  const [end, setEnd] = useState('')
  const [error, setError] = useState('')
  const [warning, setWarning] = useState('')
  // Recibo do que acabou de ser programado: a mesma emissora do seletor, com o horário.
  const [confirmed, setConfirmed] = useState(null)
  const create = useCreateRecording()
  const testStream = useStationConnectionTest()

  const stationId = selection?.kind === 'station' ? selection.id : null
  // Escolheu cidade, UF ou busca livre: o combobox aceita, mas gravação é por emissora.
  const wrongKind = Boolean(selection) && !stationId
  const picked = stationId ? pickedStation(selection) : null
  const ready = Boolean(stationId && day && start && end)

  async function submit(e) {
    e.preventDefault()
    setError('')
    setWarning('')
    setConfirmed(null)
    let created
    try {
      created = await create.mutateAsync({ station_id: stationId, day, start, end })
    } catch (err) {
      setError(errorMessage(err))
      return
    }
    setConfirmed({ station: picked, window: windowLabel(created) })
    setStart('')
    setEnd('')
    // Falha aqui avisa e não bloqueia: dá tempo de corrigir a URL até o horário.
    try {
      const r = await testStream.mutateAsync({ id: created.station_id, tests: ['stream'] })
      if (r.results?.stream?.status !== 'ok') {
        setWarning('O stream desta emissora não respondeu agora. Confira a URL antes do horário.')
      }
    } catch {
      setWarning('Não consegui testar o stream agora. Confira a URL antes do horário.')
    }
  }

  return (
    <form onSubmit={submit}>
      <div className="flow-filters flow-filters--auto ar-form">
        <div className={stepClass(stationId)}>
          <label className="flow-filter-label">
            <span className="flow-filter-label-step">1</span>
            Emissora
          </label>
          <StationSearch value={selection} onChange={setSelection} placeholder="Buscar emissora…" />
          {picked && <StationIdentity station={picked} className="ar-picked" />}
          {wrongKind && (
            <p className="ar-field-hint" role="status">Escolha uma emissora da lista, não uma cidade ou estado.</p>
          )}
        </div>
        <div className={stepClass(day)}>
          <label className="flow-filter-label" htmlFor="ar-day">
            <span className="flow-filter-label-step">2</span>
            Dia
          </label>
          <input id="ar-day" className="flow-input" type="date" value={day} onChange={e => setDay(e.target.value)} />
        </div>
        <div className={stepClass(start)}>
          <label className="flow-filter-label" htmlFor="ar-start">
            <span className="flow-filter-label-step">3</span>
            Início
          </label>
          <input id="ar-start" className="flow-input" type="time" value={start} onChange={e => setStart(e.target.value)} />
        </div>
        <div className={stepClass(end)}>
          <label className="flow-filter-label" htmlFor="ar-end">
            <span className="flow-filter-label-step">4</span>
            Fim
          </label>
          <input id="ar-end" className="flow-input" type="time" value={end} onChange={e => setEnd(e.target.value)} />
        </div>
        {/* Rótulo vazio só para o botão cair na mesma altura dos controles. */}
        <div className="flow-filter">
          <span className="flow-filter-label" aria-hidden="true" />
          <button type="submit" className="btn btn-primary ar-submit" disabled={!ready || create.isPending}>
            {create.isPending ? 'Programando…' : 'Programar'}
          </button>
        </div>
      </div>
      <p className="ar-hint">
        Horário de Brasília. Até 6 horas, no mesmo dia. O áudio fica disponível por 15 dias.
        O stream chega com atraso em relação ao ar: marque alguns minutos de folga antes e depois do horário.
      </p>
      {error && <p className="ar-msg ar-msg--error" role="alert">{error}</p>}
      {confirmed && (
        <div className={`ar-confirm${warning ? ' ar-confirm--warn' : ''}`} role="status">
          <span className="ar-confirm-mark"><IconCheck /></span>
          <div className="ar-confirm-body">
            <p className="ar-confirm-title">Gravação programada</p>
            <StationIdentity station={confirmed.station} />
            <p className="ar-confirm-when">{confirmed.window}</p>
            {warning && <p className="ar-confirm-warn">{warning}</p>}
          </div>
          <button
            type="button"
            className="btn btn-icon btn-sm ar-confirm-close"
            aria-label="Fechar aviso"
            title="Fechar"
            onClick={() => { setConfirmed(null); setWarning('') }}
          >
            <IconX />
          </button>
        </div>
      )}
    </form>
  )
}

function RecordingRow({ rec }) {
  const confirm = useConfirm()
  const alert = useAlert()
  const cancelRec = useCancelRecording()
  const deleteRec = useDeleteRecording()
  const [audioUrl, setAudioUrl] = useState('')
  const [playing, setPlaying] = useState(false)
  const [busy, setBusy] = useState(false)

  useEffect(() => () => { if (audioUrl) URL.revokeObjectURL(audioUrl) }, [audioUrl])

  const info = statusInfo(rec.status)
  const coverage = coveragePercent(rec)
  // Buraco é prova em qualquer gravação com áudio (inclusive cancelada), não só na parcial.
  const gaps = rec.has_audio ? gapLabels(rec) : []
  const showError = (rec.status === 'falhou' || rec.status === 'processando') && Boolean(rec.last_error)
  const canCancel = rec.status === 'programada' || rec.status === 'gravando'
  const canDelete = !['programada', 'gravando', 'processando'].includes(rec.status)

  async function listen() {
    if (playing) {
      setPlaying(false)
      return
    }
    if (audioUrl) {
      setPlaying(true)
      return
    }
    setBusy(true)
    try {
      const blob = await fetchRecordingAudio(rec.id)
      setAudioUrl(URL.createObjectURL(blob))
      setPlaying(true)
    } catch (err) {
      await alert(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  async function download() {
    setBusy(true)
    try {
      const blob = await fetchRecordingAudio(rec.id)
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = downloadName(rec)
      document.body.appendChild(a)
      a.click()
      a.remove()
      // Blob grande: revogar na hora pode abortar o download antes de ele começar.
      setTimeout(() => URL.revokeObjectURL(url), 10000)
    } catch (err) {
      await alert(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  async function cancel() {
    const live = rec.status === 'gravando'
    const ok = await confirm(live
      ? 'Parar esta gravação agora? O que já foi gravado fica salvo.'
      : 'Cancelar esta gravação programada?')
    if (!ok) return
    try { await cancelRec.mutateAsync(rec.id) } catch (err) { await alert(errorMessage(err)) }
  }

  async function remove() {
    const ok = await confirm('Excluir esta gravação? O áudio será apagado e não dá para recuperar.')
    if (!ok) return
    try { await deleteRec.mutateAsync(rec.id) } catch (err) { await alert(errorMessage(err)) }
  }

  return (
    <>
      <tr>
        <td><StationIdentity station={recordingStation(rec)} size={30} /></td>
        <td>{windowLabel(rec)}</td>
        <td><span className={`badge ${info.badge}`}>{info.label}</span></td>
        <td>{coverage == null ? '—' : `${coverage}%`}</td>
        <td>{expiresLabel(rec)}</td>
        <td>
          {/* title é a dica do mouse; aria-label é o nome do botão para leitor de tela. */}
          <div className="ar-actions">
            {rec.has_audio && (
              <>
                <button
                  type="button"
                  className={`btn btn-icon btn-sm${playing ? ' ar-icon--on' : ''}`}
                  onClick={listen}
                  disabled={busy}
                  title={playing ? 'Pausar' : 'Ouvir'}
                  aria-label={playing ? 'Pausar' : 'Ouvir'}
                >
                  {playing ? <IconPause /> : <IconPlay />}
                </button>
                <button type="button" className="btn btn-icon btn-sm" onClick={download} disabled={busy} title="Baixar" aria-label="Baixar">
                  <IconDownload />
                </button>
                <Link
                  to={`/admin/gravacoes/${rec.id}/editar`}
                  className="btn btn-icon btn-sm"
                  title="Editar trechos (cortar e exportar só a parte que interessa)"
                  aria-label="Editar trechos"
                >
                  <IconPencil />
                </Link>
              </>
            )}
            {canCancel && (
              <button
                type="button"
                className="btn btn-icon btn-danger-ghost btn-sm"
                onClick={cancel}
                disabled={cancelRec.isPending}
                title={rec.status === 'gravando' ? 'Parar gravação' : 'Cancelar gravação'}
                aria-label={rec.status === 'gravando' ? 'Parar gravação' : 'Cancelar gravação'}
              >
                {rec.status === 'gravando' ? <IconStop /> : <IconX />}
              </button>
            )}
            {canDelete && (
              <button
                type="button"
                className="btn btn-icon btn-danger-ghost btn-sm"
                onClick={remove}
                disabled={deleteRec.isPending}
                title="Excluir"
                aria-label="Excluir"
              >
                <IconTrash />
              </button>
            )}
          </div>
        </td>
      </tr>
      {(audioUrl || gaps.length > 0 || showError) && (
        <tr className="ar-detail">
          <td colSpan={6}>
            {audioUrl && (
              <AudioPlayer src={audioUrl} isPlaying={playing} onPlay={() => setPlaying(true)} onPause={() => setPlaying(false)} />
            )}
            {gaps.length > 0 && (
              <ul className="ar-gaps">{gaps.map(g => <li key={g}>{g}</li>)}</ul>
            )}
            {showError && <p className="ar-error-detail">{rec.last_error}</p>}
          </td>
        </tr>
      )}
    </>
  )
}

const ICON_REC = (
  <svg width="22" height="22" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true">
    <circle cx="8" cy="8" r="6" />
    <circle cx="8" cy="8" r="2.4" fill="currentColor" stroke="none" />
  </svg>
)

// Silhueta da tabela: linhas de skeleton na proporção das colunas reais.
function GhostRows() {
  return (
    <div>
      {[0, 1, 2, 3].map(i => (
        <div key={i} className="skeleton-row">
          <span className="skeleton-cell" style={{ width: '22%' }} />
          <span className="skeleton-cell" style={{ width: '28%' }} />
          <span className="skeleton-cell" style={{ width: '12%' }} />
          <span className="skeleton-cell" style={{ width: '8%' }} />
          <span className="skeleton-cell" style={{ width: '10%' }} />
        </div>
      ))}
    </div>
  )
}

export default function AdminRecordingsPage() {
  const { data, isLoading, isError } = useRecordings()
  const items = data?.items ?? []
  const total = data?.total ?? 0

  return (
    <div>
      <div className="page-header">
        <h1>Gravações</h1>
      </div>

      <RecordingForm />

      {isError && <p className="ar-msg ar-msg--error">Não foi possível carregar as gravações.</p>}
      {!isLoading && !isError && items.length === 0 && (
        <FlowEmptyState
          className="ar-none"
          tone="mute"
          icon={ICON_REC}
          title={<>Nenhuma <strong>gravação</strong> ainda</>}
          description="Escolha a emissora, o dia e o horário acima e clique em Programar. As gravações aparecem aqui."
          ghost={<GhostRows />}
        />
      )}
      {items.length > 0 && (
        <div className="ar-table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>Emissora</th>
                <th>Horário</th>
                <th>Status</th>
                <th>Cobertura</th>
                <th>Expira em</th>
                <th aria-label="Ações" />
              </tr>
            </thead>
            <tbody>
              {items.map(r => <RecordingRow key={r.id} rec={r} />)}
            </tbody>
          </table>
          {total > items.length && (
            <p className="ar-hint">Mostrando as {items.length} mais recentes de {total}.</p>
          )}
        </div>
      )}
    </div>
  )
}
