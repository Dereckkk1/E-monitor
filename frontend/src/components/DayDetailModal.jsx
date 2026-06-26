import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useDetections, useCreateManualBatchDetection } from '../api/hooks'
import { useAuth } from '../contexts/AuthContext'
import BadgePill from './BadgePill'
import AudioPlayer from './AudioPlayer'
import api from '../api/client'

const CATEGORY_LABEL = {
  in_slot:  { label: 'Dentro da faixa', variant: 'green' },
  out_slot: { label: 'Fora da faixa',   variant: 'yellow' },
  out_date: { label: 'Fora da data',    variant: 'purple' },
  orphan:   { label: 'Bônus (sem regra)', variant: 'blue' },
}

function fmtTime(iso) {
  const d = new Date(iso)
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  const ss = String(d.getSeconds()).padStart(2, '0')
  return `${hh}:${mm}:${ss}`
}

function fmtDate(iso) {
  const [y, m, d] = iso.slice(0, 10).split('-')
  return `${d}/${m}/${y}`
}

// parseTimeFromName: tenta extrair um horário do nome do arquivo de censura
// (best-effort, sempre conferido pelo operador). Prioriza padrões com separador
// de hora explícito (06h57, 06:57) pra não confundir com datas; depois 6 dígitos
// colados (065700 = HH:MM:SS) e 4 dígitos colados (0657 = HH:MM). Devolve
// 'HH:MM:SS' válido ou null. Ranges fora de 23:59:59 são rejeitados.
function parseTimeFromName(name) {
  const base = String(name || '').replace(/\.[^.]+$/, '')
    // remove trechos com cara de data (ISO e DD-MM-AAAA) pra não ler ano/dia como hora
    .replace(/\b\d{4}[-/.]\d{2}[-/.]\d{2}\b/g, ' ')
    .replace(/\b\d{2}[-/.]\d{2}[-/.]\d{4}\b/g, ' ')
  const pad = n => String(n).padStart(2, '0')
  const tryRe = (re) => {
    let m
    while ((m = re.exec(base)) !== null) {
      const h = +m[1], mi = +m[2], s = m[3] != null ? +m[3] : 0
      if (h <= 23 && mi <= 59 && s <= 59) return `${pad(h)}:${pad(mi)}:${pad(s)}`
    }
    return null
  }
  return tryRe(/(\d{1,2})\s*[h:]\s*(\d{2})(?:\s*[m:]?\s*(\d{2}))?/gi)   // 06h57 / 06:57[:30]
      || tryRe(/(?:^|\D)(\d{2})(\d{2})(\d{2})(?:\D|$)/g)               // 065700
      || tryRe(/(?:^|\D)(\d{2})(\d{2})(?:\D|$)/g)                      // 0657
}

/**
 * Day-detail modal for the /detections grid.
 *
 * Props:
 *  - campaignId: uuid
 *  - stationId: uuid
 *  - materialId: uuid (same UUID as commercial_id in detections)
 *  - dateISO: 'YYYY-MM-DD'
 *  - station: station object (for displaying name)
 *  - material: material object (for displaying title)
 *  - cellSummary: row from daily_play_summary view ({expected, in_slot, deficit, bonus, out_slot, out_date})
 *  - onClose: () => void
 */
export default function DayDetailModal({
  campaignId, stationId, typeId, dateISO,
  station, materialType, cellSummary, rules = [], availableMaterials = [],
  onClose,
}) {
  const { isAdmin } = useAuth()
  const [activePlayerId, setActivePlayerId] = useState(null)
  const [showManualForm, setShowManualForm] = useState(false)
  // Blob URLs keyed by detection id. Using a ref for synchronous cache
  // lookups and state for triggering re-renders.
  const [evidenceBlobUrls, setEvidenceBlobUrls] = useState({})
  const blobUrlsRef = useRef({})
  const [loadingId, setLoadingId] = useState(null)

  // Esc to close
  useEffect(() => {
    function onKey(e) { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  // Revoke all blob URLs when the modal unmounts to free memory.
  useEffect(() => {
    return () => {
      Object.values(blobUrlsRef.current).forEach(u => URL.revokeObjectURL(u))
    }
  }, [])

  // ensureEvidenceUrl fetches the audio blob from the API proxy endpoint
  // on first call, caches the resulting blob URL, and returns it on
  // subsequent calls. Using the proxy avoids presigned MinIO URLs (which
  // break in prod due to mixed-content / localhost addressing).
  const ensureEvidenceUrl = useCallback(async (id) => {
    if (blobUrlsRef.current[id]) return blobUrlsRef.current[id]
    const resp = await api.get(`/detections/${id}/evidence`, { responseType: 'blob' })
    const blobUrl = URL.createObjectURL(resp.data)
    blobUrlsRef.current[id] = blobUrl
    setEvidenceBlobUrls(prev => ({ ...prev, [id]: blobUrl }))
    return blobUrl
  }, [])

  async function handlePlay(id) {
    if (loadingId) return
    if (evidenceBlobUrls[id]) {
      setActivePlayerId(id)
      return
    }
    setLoadingId(id)
    try {
      await ensureEvidenceUrl(id)
      setActivePlayerId(id)
    } catch {
      // Swallow: next click retries. AudioPlayer with no src renders idle.
    } finally {
      setLoadingId(null)
    }
  }

  async function handleDownload(id) {
    try {
      const url = await ensureEvidenceUrl(id)
      const a = document.createElement('a')
      a.href = url
      a.download = `veiculacao-${id}.m4a`
      document.body.appendChild(a)
      a.click()
      a.remove()
    } catch {
      // Same rationale as play: silent failure, user can retry.
    }
  }

  // Fetch detections for this (campaign, station, day) using São Paulo local-day
  // range (UTC-3). The daily_play_summary view buckets by SP day; matching that
  // here ensures we get the same detections the view counted.
  const startISO = new Date(`${dateISO}T00:00:00.000-03:00`).toISOString()
  const endISO   = new Date(`${dateISO}T23:59:59.999-03:00`).toISOString()
  const { data: detectionsResp, isLoading } = useDetections({
    campaign_id: campaignId,
    station_id: stationId,
    start_date: startISO,
    end_date: endISO,
    limit: 200,
  })

  // useDetections may return either an array directly or {data: [...]}
  const detections = Array.isArray(detectionsResp)
    ? detectionsResp
    : (detectionsResp?.data ?? [])

  // Migration 0019: rows are by TYPE, so the modal filters detections to those
  // whose material's type matches. The backend enriches each detection with
  // type_id via JOIN materials (catalog/detections.go).
  const filtered = detections.filter(d => d.type_id === typeId)
  const grouped = {
    in_slot:  filtered.filter(d => d.category === 'in_slot'),
    out_slot: filtered.filter(d => d.category === 'out_slot'),
    out_date: filtered.filter(d => d.category === 'out_date'),
    orphan:   filtered.filter(d => d.category === 'orphan'),
  }

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div
        className="modal"
        style={{
          maxWidth: 700,
          maxHeight: 'calc(100vh - 48px)',
          display: 'flex', flexDirection: 'column',
          overflow: 'hidden',
        }}
        onClick={e => e.stopPropagation()}
      >
        <div className="modal-header" style={{ flexShrink: 0 }}>
          <div style={{ minWidth: 0 }}>
            <h3 style={{ margin: 0, display: 'flex', alignItems: 'center', gap: 8 }}>
              {materialType?.color && (
                <span style={{
                  display: 'inline-block', width: 10, height: 10, borderRadius: 3,
                  background: materialType.color, flexShrink: 0,
                }} />
              )}
              {materialType?.name ?? 'Tipo'} · {station?.name ?? 'Emissora'}
            </h3>
            <p style={{ margin: '4px 0 0', color: '#64748b', fontSize: 13 }}>
              {fmtDate(dateISO)} — materiais individuais detectados abaixo
            </p>
            {rules.length > 0 && (
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 8 }}>
                {rules.map(r => (
                  <span key={r.id} style={{
                    display: 'inline-flex', alignItems: 'center', gap: 6,
                    fontSize: 11, fontWeight: 600,
                    padding: '3px 8px', borderRadius: 999,
                    background: '#fdf2f8', color: '#be185d',
                    border: '1px solid #fbcfe8',
                  }}>
                    <svg width="11" height="11" viewBox="0 0 16 16" fill="none" aria-hidden>
                      <circle cx="8" cy="8" r="6.25" stroke="currentColor" strokeWidth="1.4" />
                      <path d="M8 4.5V8l2.25 1.5" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
                    </svg>
                    {r.time_start.slice(0, 5)}–{r.time_end.slice(0, 5)}
                    <span style={{ color: '#9d174d', fontWeight: 500, opacity: 0.75 }}>
                      · {r.plays_per_day}×/dia
                    </span>
                  </span>
                ))}
              </div>
            )}
          </div>
          <button className="modal-close" onClick={onClose} type="button">×</button>
        </div>

        <div className="modal-body" style={{ padding: 20, flex: 1, overflowY: 'auto', minHeight: 0 }}>

          {/* Cell summary badges */}
          {cellSummary && (
            <div style={{
              display: 'flex', gap: 12, flexWrap: 'wrap', marginBottom: 20,
              padding: 14, background: '#fafbfc', borderRadius: 8, border: '1px solid #e2e8f0',
            }}>
              <SummaryStat label="Esperado" value={cellSummary.expected} variant="gray" />
              <SummaryStat label="Tocou (faixa)" value={cellSummary.in_slot} variant="green" />
              {cellSummary.deficit > 0 && (
                <SummaryStat label="Faltou" value={cellSummary.deficit} variant="red" />
              )}
              {cellSummary.bonus > 0 && (
                <SummaryStat label="Bônus" value={cellSummary.bonus} variant="blue" prefix="+" />
              )}
              {cellSummary.out_slot > 0 && (
                <SummaryStat label="Fora faixa" value={cellSummary.out_slot} variant="yellow" prefix="+" />
              )}
              {cellSummary.out_date > 0 && (
                <SummaryStat label="Fora data" value={cellSummary.out_date} variant="purple" prefix="+" />
              )}
            </div>
          )}

          {isLoading ? (
            <p style={{ color: '#64748b' }}>Carregando detecções…</p>
          ) : filtered.length === 0 ? (
            <p style={{ color: '#64748b' }}>Nenhuma detection registrada nesse dia.</p>
          ) : (
            <DetectionsList
              grouped={grouped}
              materialType={materialType}
              activePlayerId={activePlayerId}
              evidenceBlobUrls={evidenceBlobUrls}
              loadingId={loadingId}
              onPlay={handlePlay}
              onPause={() => setActivePlayerId(null)}
              onDownload={handleDownload}
            />
          )}

          {/* Admin-only manual entry. Aparece tanto no empty-state quanto
              após a lista carregada. */}
          {isAdmin && !isLoading && (
            <div style={{ marginTop: 22, paddingTop: 18, borderTop: '1px dashed var(--c-border)' }}>
              {showManualForm ? (
                <ManualEntryForm
                  campaignId={campaignId}
                  stationId={stationId}
                  dateISO={dateISO}
                  availableMaterials={availableMaterials}
                  materialType={materialType}
                  station={station}
                  onCancel={() => setShowManualForm(false)}
                  onSaved={() => setShowManualForm(false)}
                />
              ) : (
                <OpenManualFormCTA onClick={() => setShowManualForm(true)} />
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

// ── Manual entry: open CTA ──────────────────────────────────────
// Pílula ghost com borda tracejada que se aquece pro rosa-acao no hover.
// Segue o padrão de "ações de adição" do design system: ghost no estado neutro,
// action-tinted no hover. translateY(-1px) + shadow lift no hover dá a sensação
// de elevação descrita na §4.1 do design.md.
function OpenManualFormCTA({ onClick }) {
  const [hover, setHover] = useState(false)
  return (
    <button
      type="button"
      onClick={onClick}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      style={{
        display: 'inline-flex', alignItems: 'center', gap: 9,
        padding: '10px 16px',
        borderRadius: 'var(--radius-md)',
        border: `1px dashed ${hover ? 'var(--c-action)' : '#cbd5e1'}`,
        background: hover ? 'var(--c-action-light)' : '#fafbfc',
        color: hover ? '#9d174d' : '#475569',
        fontSize: 12.5, fontWeight: 600,
        fontFamily: 'var(--font-heading)',
        letterSpacing: '0.01em',
        cursor: 'pointer',
        transition: 'transform 140ms cubic-bezier(0.16,1,0.3,1), background 140ms, border-color 140ms, color 140ms, box-shadow 140ms',
        transform: hover ? 'translateY(-1px)' : 'translateY(0)',
        boxShadow: hover ? 'var(--shadow-sm)' : 'none',
      }}
    >
      <span
        style={{
          display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
          width: 20, height: 20, borderRadius: 'var(--radius-full)',
          background: hover ? 'var(--c-action)' : '#e2e8f0',
          color: hover ? '#fff' : '#64748b',
          transition: 'all 140ms',
        }}
        aria-hidden
      >
        <svg width="11" height="11" viewBox="0 0 16 16" fill="none">
          <path d="M8 3v10M3 8h10" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
        </svg>
      </span>
      Adicionar veiculação manualmente
    </button>
  )
}

// ── Manual entry: form ──────────────────────────────────────────
// Formulário inline com header destacado em rosa-action, grid 2-col pra
// material+horário em telas largas, textarea e dropzone full-width. Foco usa
// aura rosa (box-shadow 0 0 0 3px var(--c-action-light)) — assinatura do
// design system descrita na §4.5 do design.md.
function ManualEntryForm({
  campaignId, stationId, dateISO, availableMaterials, materialType, station,
  onCancel, onSaved,
}) {
  const firstMat = availableMaterials[0]?.id ?? ''
  const rowSeq = useRef(0)
  const mkRow = useCallback((time = '12:00', materialId = firstMat) => ({
    key: rowSeq.current++, materialId, time, note: '', audio: null, audioError: '',
  }), [firstMat])
  const [rows, setRows] = useState(() => [mkRow()])
  const [proof, setProof] = useState(null)
  const [proofError, setProofError] = useState('')
  const [rowErrors, setRowErrors] = useState({}) // índice da linha -> mensagem (vinda do 422)
  const [bulkMaterial, setBulkMaterial] = useState(firstMat) // "aplicar material a todas as linhas"
  const createBatch = useCreateManualBatchDetection()
  const noMaterials = availableMaterials.length === 0
  const MAX_MB = 25
  const AUDIO_MIME = ['audio/mpeg', 'audio/mp3', 'audio/mp4', 'audio/x-m4a',
                      'audio/aac', 'audio/wav', 'audio/x-wav', 'audio/wave', 'audio/ogg']

  function patchRow(key, patch) {
    setRows(rs => rs.map(r => (r.key === key ? { ...r, ...patch } : r)))
  }
  function addRow() {
    setRows(rs => [...rs, mkRow(rs[rs.length - 1]?.time ?? '12:00', bulkMaterial)])
  }
  // Aplica o mesmo material a todas as linhas (e vira o default das próximas).
  function applyBulkMaterial(value) {
    setBulkMaterial(value)
    setRows(rs => rs.map(r => ({ ...r, materialId: value })))
  }
  // Fluxo "áudio-first": seleciona/solta N censuras de uma vez -> N linhas, cada
  // uma com seu áudio, horário pré-preenchido pelo nome do arquivo (best-effort)
  // e material = bulkMaterial. Arquivos inválidos (tipo/tamanho) são ignorados.
  function addAudioRows(fileList) {
    const files = Array.from(fileList || [])
    if (files.length === 0) return
    let skipped = 0
    const fresh = []
    for (const f of files) {
      if (f.size > MAX_MB * 1024 * 1024) { skipped++; continue }
      if (f.type && !AUDIO_MIME.includes(f.type.toLowerCase())) { skipped++; continue }
      fresh.push({ ...mkRow(parseTimeFromName(f.name) ?? '12:00', bulkMaterial), audio: f })
    }
    if (fresh.length > 0) {
      setRows(rs => {
        const pristine = rs.length === 1 && !rs[0].audio && rs[0].note.trim() === '' && rs[0].time === '12:00'
        return pristine ? fresh : [...rs, ...fresh]
      })
    }
    if (skipped > 0) {
      window.alert(`${skipped} arquivo(s) ignorado(s): formato não suportado ou acima de ${MAX_MB}MB.`)
    }
  }
  function removeRow(key) {
    setRows(rs => (rs.length > 1 ? rs.filter(r => r.key !== key) : rs))
    setRowErrors({}) // erros do 422 são por índice; ao remover linha os índices deslocam — limpa pra não apontar pra linha errada
  }
  function pickRowAudio(key, file) {
    if (!file) { patchRow(key, { audio: null, audioError: '' }); return }
    if (file.size > MAX_MB * 1024 * 1024) { patchRow(key, { audio: null, audioError: `Acima de ${MAX_MB}MB.` }); return }
    if (file.type && !AUDIO_MIME.includes(file.type.toLowerCase())) {
      patchRow(key, { audio: null, audioError: 'Formato inválido (mp3, m4a, wav, aac, ogg).' }); return
    }
    patchRow(key, { audio: file, audioError: '' })
  }
  function pickProof(file) {
    if (!file) { setProof(null); setProofError(''); return }
    if (file.size > MAX_MB * 1024 * 1024) { setProof(null); setProofError(`Acima de ${MAX_MB}MB.`); return }
    if (file.type && file.type.toLowerCase() !== 'application/pdf') {
      setProof(null); setProofError('O comprovante precisa ser PDF.'); return
    }
    setProof(file); setProofError('')
  }

  async function submit(e) {
    e.preventDefault()
    setRowErrors({})
    const missing = rows.findIndex(r => !r.materialId)
    if (missing >= 0) { setRowErrors({ [missing]: 'Selecione o material.' }); return }
    const entries = rows.map(r => ({
      commercial_id: r.materialId,
      detected_at: new Date(`${dateISO}T${r.time.length === 5 ? r.time + ':00' : r.time}-03:00`).toISOString(),
      note: r.note.trim(),
    }))
    const audios = {}
    rows.forEach((r, i) => { if (r.audio) audios[i] = r.audio })
    try {
      const res = await createBatch.mutateAsync({
        campaign_id: campaignId,
        station_id: stationId,
        entries,
        proof: proof ?? undefined,
        audios,
      })
      const warns = res?.warnings?.length ?? 0
      onSaved()
      if (warns > 0) {
        window.alert(`${rows.length} veiculação(ões) criada(s). ${warns} aviso(s) no upload de mídia. Confira na detail page de cada uma.`)
      }
    } catch (err) {
      const status = err?.response?.status
      const errs = err?.response?.data?.errors
      if (status === 422 && Array.isArray(errs)) {
        const map = {}
        errs.forEach(x => { map[x.index] = x.message })
        setRowErrors(map)
        return
      }
      window.alert(
        status === 413 ? 'Arquivo acima de 25MB.'
        : status === 415 ? 'Formato não suportado (PDF no comprovante; mp3/m4a/wav/aac/ogg no áudio).'
        : status === 403 ? 'Apenas administradores podem inserir veiculações manualmente.'
        : 'Erro ao salvar. Tente novamente.'
      )
    }
  }

  if (noMaterials) {
    return <NoMaterialsState materialType={materialType} station={station} onCancel={onCancel} />
  }

  return (
    <form
      onSubmit={submit}
      style={{
        position: 'relative',
        background: 'var(--c-surface)',
        border: '1px solid var(--c-border)',
        borderRadius: 'var(--radius-lg)',
        boxShadow: 'var(--shadow-sm)',
        overflow: 'hidden',
      }}
    >
      {/* Header com accent rosa à esquerda */}
      <header style={{
        display: 'flex', alignItems: 'flex-start', gap: 12,
        padding: '14px 18px 14px 14px',
        background: 'linear-gradient(180deg, var(--c-action-light) 0%, rgba(252,231,243,0.35) 100%)',
        borderBottom: '1px solid var(--c-border)',
      }}>
        <span style={{
          width: 3, alignSelf: 'stretch', borderRadius: 'var(--radius-full)',
          background: 'var(--c-action)', flexShrink: 0,
        }} />
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <span style={{
            fontSize: 9.5, fontWeight: 700, letterSpacing: '0.14em',
            color: 'var(--c-action)', textTransform: 'uppercase',
            fontFamily: 'var(--font-heading)',
          }}>
            Inserção retroativa em lote
          </span>
          <span style={{
            fontSize: 14, fontWeight: 700, color: 'var(--c-text)',
            fontFamily: 'var(--font-heading)', letterSpacing: '-0.01em',
          }}>
            Adicionar veiculações
          </span>
          <span style={{ fontSize: 11.5, color: 'var(--c-text-2)', lineHeight: 1.45, marginTop: 2 }}>
            Suba várias de uma vez: material, horário e descrição por linha. O
            comprovante (PDF) é opcional e cobre o lote; o áudio da censura pode
            vir agora ou depois, em cada veiculação.
          </span>
        </div>
      </header>

      {/* Body: comprovante + linhas */}
      <div style={{
        display: 'flex', flexDirection: 'column', gap: 14,
        padding: '16px 18px 18px',
      }}>
        <Field as="div" label="Comprovante (PDF)" hint="Opcional. Um comprovante cobre todas as linhas abaixo; sem ele as veiculações são criadas normalmente.">
          <ProofDropzone
            proof={proof}
            error={proofError}
            onFile={pickProof}
            onClear={() => { setProof(null); setProofError('') }}
          />
        </Field>

        <Field as="div" label="Censuras (áudios)" hint="Selecione vários de uma vez: cada arquivo vira uma linha, com o horário pré-preenchido pelo nome quando dá pra reconhecer (ex.: '0657', '06h57'). Confira sempre.">
          <CensurasDropzone onFiles={addAudioRows} />
        </Field>

        {availableMaterials.length > 1 && rows.length > 1 && (
          <Field label="Material de todas as linhas" hint="Aplica o mesmo material a todas; ainda dá pra ajustar linha a linha.">
            <StyledSelect value={bulkMaterial} onChange={e => applyBulkMaterial(e.target.value)}>
              {availableMaterials.map(m => (
                <option key={m.id} value={m.id}>
                  {m.title || m.name || 'Sem título'}{m.duration_seconds ? ` · ${m.duration_seconds}s` : ''}
                </option>
              ))}
            </StyledSelect>
          </Field>
        )}

        <div>
          {rows.map((row, i) => (
            <BatchRow
              key={row.key}
              index={i}
              row={row}
              materials={availableMaterials}
              error={rowErrors[i]}
              canRemove={rows.length > 1}
              onChange={patch => patchRow(row.key, patch)}
              onPickAudio={file => pickRowAudio(row.key, file)}
              onRemove={() => removeRow(row.key)}
            />
          ))}
        </div>

        <AddRowButton onClick={addRow} />
      </div>

      {/* Footer com ações */}
      <footer style={{
        display: 'flex', gap: 8, justifyContent: 'flex-end',
        padding: '12px 18px',
        background: 'var(--c-surface-2)',
        borderTop: '1px solid var(--c-border)',
      }}>
        <GhostButton type="button" onClick={onCancel} disabled={createBatch.isPending}>
          Cancelar
        </GhostButton>
        <PrimaryButton type="submit" disabled={createBatch.isPending}>
          {createBatch.isPending ? (
            <>
              <Spinner /> Salvando…
            </>
          ) : (
            <>
              <svg width="13" height="13" viewBox="0 0 16 16" fill="none" aria-hidden>
                <path d="M3 8.5l3 3 7-7" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
              Salvar {rows.length} veiculação{rows.length > 1 ? 'ões' : ''}
            </>
          )}
        </PrimaryButton>
      </footer>
    </form>
  )
}

// ── Pieces ──────────────────────────────────────────────────────

// `as` permite renderizar o wrapper como <div> em vez de <label>. Necessário
// pro campo de áudio: a dropzone já dispara inputRef.current.click() no onClick,
// e um <label> ao redor REPASSARIA o mesmo clique pro <input type="file">,
// abrindo o seletor de arquivo duas vezes.
function Field({ label, required, hint, children, as: Tag = 'label' }) {
  return (
    <Tag style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
      <span style={{
        display: 'flex', alignItems: 'center', gap: 4,
        fontSize: 10.5, fontWeight: 700, letterSpacing: '0.06em',
        color: 'var(--c-text-2)', textTransform: 'uppercase',
        fontFamily: 'var(--font-heading)',
      }}>
        {label}
        {required && <span style={{ color: 'var(--c-action)' }}>*</span>}
      </span>
      {children}
      {hint && (
        <span style={{ fontSize: 11, color: 'var(--c-text-3)', lineHeight: 1.45 }}>
          {hint}
        </span>
      )}
    </Tag>
  )
}

// StyledInput + StyledSelect: inputs com aura de focus rosa. inline-style não
// suporta :focus, então usamos onFocus/onBlur pra alternar border+box-shadow.
function StyledInput({ as = 'input', style, ...props }) {
  const [focused, setFocused] = useState(false)
  const baseStyle = {
    width: '100%', boxSizing: 'border-box',
    padding: '9px 11px',
    border: `1px solid ${focused ? 'var(--c-action)' : 'var(--c-border)'}`,
    borderRadius: 'var(--radius-md)',
    background: 'var(--c-surface)',
    fontSize: 13, color: 'var(--c-text)',
    fontFamily: 'var(--font-heading)', fontWeight: 500,
    outline: 'none',
    transition: 'border-color 140ms, box-shadow 140ms',
    boxShadow: focused ? '0 0 0 3px var(--c-action-light)' : 'none',
    resize: as === 'textarea' ? 'vertical' : undefined,
    ...style,
  }
  const Tag = as
  return <Tag {...props} style={baseStyle} onFocus={() => setFocused(true)} onBlur={() => setFocused(false)} />
}

function StyledSelect({ children, ...props }) {
  const [focused, setFocused] = useState(false)
  return (
    <div style={{ position: 'relative' }}>
      <select
        {...props}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
        style={{
          width: '100%', appearance: 'none',
          padding: '9px 34px 9px 11px',
          border: `1px solid ${focused ? 'var(--c-action)' : 'var(--c-border)'}`,
          borderRadius: 'var(--radius-md)',
          background: 'var(--c-surface)',
          fontSize: 13, color: 'var(--c-text)',
          fontFamily: 'var(--font-heading)', fontWeight: 500,
          outline: 'none', cursor: 'pointer',
          transition: 'border-color 140ms, box-shadow 140ms',
          boxShadow: focused ? '0 0 0 3px var(--c-action-light)' : 'none',
        }}
      >
        {children}
      </select>
      <svg
        width="11" height="11" viewBox="0 0 16 16" fill="none" aria-hidden
        style={{
          position: 'absolute', right: 12, top: '50%',
          transform: `translateY(-50%) rotate(${focused ? '180deg' : '0deg'})`,
          color: focused ? 'var(--c-action)' : 'var(--c-text-3)',
          pointerEvents: 'none',
          transition: 'transform 180ms cubic-bezier(0.16,1,0.3,1), color 140ms',
        }}
      >
        <path d="M4 6l4 4 4-4" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    </div>
  )
}

// ProofDropzone: dropzone full-width pro comprovante PDF do lote. Mesma
// linguagem da antiga dropzone de áudio — dashed border que acende em rosa no
// drag, verde com arquivo, vermelho em erro.
function ProofDropzone({ proof, error, onFile, onClear }) {
  const inputRef = useRef(null)
  const [dragOver, setDragOver] = useState(false)
  const hasFile = !!proof
  const hasError = !!error
  const accentColor = hasError ? 'var(--c-danger)' : hasFile ? 'var(--c-success)' : (dragOver ? 'var(--c-action)' : 'var(--c-border)')
  const accentBg    = hasError ? '#fef2f2' : hasFile ? '#f0fdf4' : (dragOver ? 'var(--c-action-light)' : 'var(--c-surface)')

  return (
    <div
      role="button"
      tabIndex={0}
      aria-label="Selecionar comprovante PDF"
      onClick={() => inputRef.current?.click()}
      onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); inputRef.current?.click() } }}
      onDragOver={e => { e.preventDefault(); setDragOver(true) }}
      onDragLeave={() => setDragOver(false)}
      onDrop={e => { e.preventDefault(); setDragOver(false); onFile(e.dataTransfer.files?.[0] ?? null) }}
      style={{
        display: 'flex', alignItems: 'center', gap: 12,
        padding: hasFile ? '10px 12px' : '14px 16px',
        border: `1.5px dashed ${accentColor}`,
        borderRadius: 'var(--radius-md)',
        background: accentBg,
        cursor: 'pointer',
        transition: 'all 160ms cubic-bezier(0.16,1,0.3,1)',
      }}
    >
      <input
        ref={inputRef}
        type="file"
        accept="application/pdf,.pdf"
        onChange={e => onFile(e.target.files?.[0] ?? null)}
        style={{ display: 'none' }}
      />

      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        width: 34, height: 34, flexShrink: 0,
        borderRadius: 'var(--radius-md)',
        background: hasError ? '#fee2e2' : hasFile ? '#dcfce7' : (dragOver ? 'var(--c-action)' : 'var(--c-surface-2)'),
        color:      hasError ? 'var(--c-danger)' : hasFile ? 'var(--c-success)' : (dragOver ? '#fff' : 'var(--c-text-3)'),
        transition: 'all 160ms',
      }}>
        {hasError ? (
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <circle cx="12" cy="12" r="10" /><line x1="12" y1="8" x2="12" y2="12" /><line x1="12" y1="16" x2="12.01" y2="16" />
          </svg>
        ) : hasFile ? (
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round">
            <polyline points="20 6 9 17 4 12" />
          </svg>
        ) : (
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
            <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" /><path d="M14 2v6h6" />
          </svg>
        )}
      </div>

      <div style={{ flex: 1, minWidth: 0 }}>
        {hasError ? (
          <>
            <div style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--c-danger)', fontFamily: 'var(--font-heading)' }}>{error}</div>
            <div style={{ fontSize: 11, color: 'var(--c-text-3)', marginTop: 2 }}>Clique pra escolher outro arquivo.</div>
          </>
        ) : hasFile ? (
          <>
            <div style={{
              fontSize: 12.5, fontWeight: 600, color: 'var(--c-text)',
              fontFamily: 'var(--font-heading)',
              whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
            }}>{proof.name}</div>
            <div style={{ fontSize: 11, color: 'var(--c-text-3)', marginTop: 2 }}>
              {(proof.size / 1024 / 1024).toFixed(2)} MB · pronto pra upload
            </div>
          </>
        ) : (
          <>
            <div style={{
              fontSize: 12.5, fontWeight: 600,
              color: dragOver ? 'var(--c-action)' : 'var(--c-text)',
              fontFamily: 'var(--font-heading)',
            }}>
              {dragOver ? 'Solta aqui pra carregar' : 'Selecionar comprovante (PDF)'}
            </div>
            <div style={{ fontSize: 11, color: 'var(--c-text-3)', marginTop: 2 }}>PDF · até 25MB</div>
          </>
        )}
      </div>

      {hasFile && !hasError && (
        <button
          type="button"
          onClick={e => { e.stopPropagation(); onClear() }}
          aria-label="Remover comprovante"
          style={{
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            width: 26, height: 26, borderRadius: 'var(--radius-full)',
            border: 0, background: 'transparent', color: 'var(--c-text-3)',
            cursor: 'pointer', flexShrink: 0, transition: 'all 120ms',
          }}
          onMouseEnter={e => { e.currentTarget.style.background = '#fee2e2'; e.currentTarget.style.color = 'var(--c-danger)' }}
          onMouseLeave={e => { e.currentTarget.style.background = 'transparent'; e.currentTarget.style.color = 'var(--c-text-3)' }}
        >
          <svg width="12" height="12" viewBox="0 0 16 16" fill="none">
            <path d="M3 3l10 10M13 3L3 13" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" />
          </svg>
        </button>
      )}
    </div>
  )
}

// CensurasDropzone: seleção em massa de áudios (fluxo áudio-first). Clica ->
// abre o seletor com multiple; também aceita arrastar vários. Cada arquivo vira
// uma linha (o pai trata em addAudioRows). Não guarda estado — é só o gatilho.
function CensurasDropzone({ onFiles }) {
  const inputRef = useRef(null)
  const [dragOver, setDragOver] = useState(false)
  return (
    <div
      role="button"
      tabIndex={0}
      aria-label="Selecionar censuras de áudio (vários arquivos)"
      onClick={() => inputRef.current?.click()}
      onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); inputRef.current?.click() } }}
      onDragOver={e => { e.preventDefault(); setDragOver(true) }}
      onDragLeave={() => setDragOver(false)}
      onDrop={e => { e.preventDefault(); setDragOver(false); onFiles(e.dataTransfer.files) }}
      style={{
        display: 'flex', alignItems: 'center', gap: 12, padding: '14px 16px',
        border: `1.5px dashed ${dragOver ? 'var(--c-action)' : 'var(--c-border)'}`,
        borderRadius: 'var(--radius-md)',
        background: dragOver ? 'var(--c-action-light)' : 'var(--c-surface)',
        cursor: 'pointer', transition: 'all 160ms cubic-bezier(0.16,1,0.3,1)',
      }}
    >
      <input
        ref={inputRef}
        type="file"
        multiple
        accept="audio/mpeg,audio/mp3,audio/mp4,audio/x-m4a,audio/aac,audio/wav,audio/wave,audio/x-wav,audio/ogg,.mp3,.m4a,.wav,.aac,.ogg"
        onChange={e => { onFiles(e.target.files); e.target.value = '' }}
        style={{ display: 'none' }}
      />
      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        width: 34, height: 34, flexShrink: 0, borderRadius: 'var(--radius-md)',
        background: dragOver ? 'var(--c-action)' : 'var(--c-surface-2)',
        color: dragOver ? '#fff' : 'var(--c-text-3)', transition: 'all 160ms',
      }}>
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
          <path d="M9 18V5l12-2v13" /><circle cx="6" cy="18" r="3" /><circle cx="18" cy="16" r="3" />
        </svg>
      </div>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 12.5, fontWeight: 600, color: dragOver ? 'var(--c-action)' : 'var(--c-text)', fontFamily: 'var(--font-heading)' }}>
          {dragOver ? 'Solta as censuras aqui' : 'Selecionar censuras (vários arquivos)'}
        </div>
        <div style={{ fontSize: 11, color: 'var(--c-text-3)', marginTop: 2 }}>
          Cada áudio vira uma linha. MP3, M4A, WAV, AAC ou OGG · até 25MB cada
        </div>
      </div>
    </div>
  )
}

// BatchRow: uma linha de veiculação. Separador hairline no topo (sem card
// aninhado — DESIGN.md). Material + horário na 1ª linha; descrição + áudio
// compactos abaixo; erro por linha (do 422) em vermelho.
function BatchRow({ index, row, materials, error, canRemove, onChange, onPickAudio, onRemove }) {
  return (
    <div style={{
      display: 'flex', flexDirection: 'column', gap: 8,
      padding: '12px 0',
      borderTop: index === 0 ? 'none' : '1px solid var(--c-border)',
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <span style={{
          display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
          width: 22, height: 22, flexShrink: 0, borderRadius: 'var(--radius-full)',
          background: 'var(--c-action-light)', color: 'var(--c-action)',
          fontSize: 11, fontWeight: 700, fontFamily: 'var(--font-heading)',
        }}>{index + 1}</span>
        <div style={{ flex: 1, minWidth: 0 }}>
          <StyledSelect value={row.materialId} onChange={e => onChange({ materialId: e.target.value })}>
            {materials.map(m => (
              <option key={m.id} value={m.id}>
                {m.title || m.name || 'Sem título'}{m.duration_seconds ? ` · ${m.duration_seconds}s` : ''}
              </option>
            ))}
          </StyledSelect>
        </div>
        <div style={{ width: 116, flexShrink: 0 }}>
          <StyledInput type="time" step="1" value={row.time} onChange={e => onChange({ time: e.target.value })} />
        </div>
        {canRemove && (
          <button
            type="button"
            onClick={onRemove}
            aria-label={`Remover linha ${index + 1}`}
            style={{
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              width: 28, height: 28, flexShrink: 0, borderRadius: 'var(--radius-md)',
              border: '1px solid var(--c-border)', background: 'var(--c-surface)',
              color: 'var(--c-text-3)', cursor: 'pointer', transition: 'all 120ms',
            }}
            onMouseEnter={e => { e.currentTarget.style.borderColor = 'var(--c-danger)'; e.currentTarget.style.color = 'var(--c-danger)'; e.currentTarget.style.background = '#fef2f2' }}
            onMouseLeave={e => { e.currentTarget.style.borderColor = 'var(--c-border)'; e.currentTarget.style.color = 'var(--c-text-3)'; e.currentTarget.style.background = 'var(--c-surface)' }}
          >
            <svg width="13" height="13" viewBox="0 0 16 16" fill="none">
              <path d="M3 3l10 10M13 3L3 13" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" />
            </svg>
          </button>
        )}
      </div>

      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', paddingLeft: 30 }}>
        <div style={{ flex: '1 1 200px', minWidth: 0 }}>
          <StyledInput value={row.note} onChange={e => onChange({ note: e.target.value })} placeholder="Descrição (opcional)" />
        </div>
        <RowAudio audio={row.audio} error={row.audioError} onFile={onPickAudio} onClear={() => onChange({ audio: null, audioError: '' })} />
      </div>

      {error && (
        <span style={{ paddingLeft: 30, fontSize: 11.5, fontWeight: 600, color: 'var(--c-danger)', fontFamily: 'var(--font-heading)' }}>
          {error}
        </span>
      )}
    </div>
  )
}

// RowAudio: controle compacto de áudio por linha. Pílula tracejada quando
// vazio; chip verde com nome + remover quando preenchido.
function RowAudio({ audio, error, onFile, onClear }) {
  const inputRef = useRef(null)
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 6, flex: '0 1 auto', minWidth: 0 }}>
      <input
        ref={inputRef}
        type="file"
        accept="audio/mpeg,audio/mp3,audio/mp4,audio/x-m4a,audio/aac,audio/wav,audio/x-wav,audio/ogg,.mp3,.m4a,.wav,.aac,.ogg"
        onChange={e => onFile(e.target.files?.[0] ?? null)}
        style={{ display: 'none' }}
      />
      {audio ? (
        <span style={{
          display: 'inline-flex', alignItems: 'center', gap: 6, maxWidth: 220,
          padding: '6px 8px 6px 10px', borderRadius: 'var(--radius-full)',
          background: '#f0fdf4', border: '1px solid var(--c-success)',
          color: 'var(--c-success)', fontSize: 11.5, fontWeight: 600,
          fontFamily: 'var(--font-heading)',
        }}>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M9 18V5l12-2v13" /><circle cx="6" cy="18" r="3" /><circle cx="18" cy="16" r="3" /></svg>
          <span style={{ whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{audio.name}</span>
          <button type="button" onClick={onClear} aria-label="Remover áudio"
            style={{ display: 'flex', border: 0, background: 'transparent', color: 'var(--c-success)', cursor: 'pointer', padding: 0, flexShrink: 0 }}>
            <svg width="12" height="12" viewBox="0 0 16 16" fill="none"><path d="M3 3l10 10M13 3L3 13" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" /></svg>
          </button>
        </span>
      ) : (
        <button
          type="button"
          onClick={() => inputRef.current?.click()}
          style={{
            display: 'inline-flex', alignItems: 'center', gap: 6,
            padding: '7px 12px', borderRadius: 'var(--radius-full)',
            border: `1px dashed ${error ? 'var(--c-danger)' : 'var(--c-border)'}`,
            background: error ? '#fef2f2' : 'var(--c-surface)',
            color: error ? 'var(--c-danger)' : 'var(--c-text-2)',
            fontSize: 11.5, fontWeight: 600, fontFamily: 'var(--font-heading)',
            cursor: 'pointer', whiteSpace: 'nowrap', transition: 'all 120ms',
          }}
          onMouseEnter={e => { if (!error) { e.currentTarget.style.borderColor = 'var(--c-action)'; e.currentTarget.style.color = 'var(--c-action)' } }}
          onMouseLeave={e => { if (!error) { e.currentTarget.style.borderColor = 'var(--c-border)'; e.currentTarget.style.color = 'var(--c-text-2)' } }}
        >
          <svg width="12" height="12" viewBox="0 0 16 16" fill="none"><path d="M8 3v10M3 8h10" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" /></svg>
          áudio da censura
        </button>
      )}
      {error && !audio && (
        <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--c-danger)', whiteSpace: 'nowrap' }}>{error}</span>
      )}
    </div>
  )
}

// AddRowButton: pílula ghost tracejada que acende em rosa pra adicionar linha.
function AddRowButton({ onClick }) {
  const [hover, setHover] = useState(false)
  return (
    <button
      type="button"
      onClick={onClick}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      style={{
        alignSelf: 'flex-start',
        display: 'inline-flex', alignItems: 'center', gap: 8,
        padding: '9px 14px', borderRadius: 'var(--radius-md)',
        border: `1px dashed ${hover ? 'var(--c-action)' : '#cbd5e1'}`,
        background: hover ? 'var(--c-action-light)' : 'transparent',
        color: hover ? '#9d174d' : 'var(--c-text-2)',
        fontSize: 12, fontWeight: 600, fontFamily: 'var(--font-heading)',
        cursor: 'pointer', transition: 'all 140ms cubic-bezier(0.16,1,0.3,1)',
      }}
    >
      <svg width="11" height="11" viewBox="0 0 16 16" fill="none"><path d="M8 3v10M3 8h10" stroke="currentColor" strokeWidth="2" strokeLinecap="round" /></svg>
      Adicionar linha
    </button>
  )
}

function GhostButton({ children, style, ...props }) {
  const [hover, setHover] = useState(false)
  return (
    <button
      {...props}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      style={{
        padding: '8px 16px',
        borderRadius: 'var(--radius-md)',
        border: `1px solid ${hover ? 'var(--c-text-3)' : 'var(--c-border)'}`,
        background: hover ? 'var(--c-surface)' : 'transparent',
        color: 'var(--c-text-2)',
        fontSize: 12.5, fontWeight: 600,
        fontFamily: 'var(--font-heading)', letterSpacing: '0.01em',
        cursor: props.disabled ? 'not-allowed' : 'pointer',
        opacity: props.disabled ? 0.55 : 1,
        transition: 'all 120ms',
        ...style,
      }}
    >
      {children}
    </button>
  )
}

function PrimaryButton({ children, style, ...props }) {
  const [hover, setHover] = useState(false)
  return (
    <button
      {...props}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      style={{
        display: 'inline-flex', alignItems: 'center', gap: 7,
        padding: '8px 18px',
        borderRadius: 'var(--radius-md)',
        border: 0,
        background: props.disabled ? '#fbcfe8' : (hover ? 'var(--c-action-hover)' : 'var(--c-action)'),
        color: '#fff',
        fontSize: 12.5, fontWeight: 700,
        fontFamily: 'var(--font-heading)', letterSpacing: '0.01em',
        cursor: props.disabled ? 'wait' : 'pointer',
        boxShadow: hover && !props.disabled ? 'var(--shadow-md)' : 'var(--shadow-sm)',
        transform: hover && !props.disabled ? 'translateY(-1px)' : 'translateY(0)',
        transition: 'all 140ms cubic-bezier(0.16,1,0.3,1)',
        ...style,
      }}
    >
      {children}
    </button>
  )
}

function Spinner() {
  return (
    <svg width="12" height="12" viewBox="0 0 16 16" fill="none"
      style={{ animation: 'rc-spin 0.9s linear infinite' }}>
      <circle cx="8" cy="8" r="6" stroke="currentColor" strokeWidth="1.8" strokeOpacity="0.3" />
      <path d="M14 8a6 6 0 0 0-6-6" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
      <style>{`@keyframes rc-spin { to { transform: rotate(360deg); } }`}</style>
    </svg>
  )
}

// Estado vazio: nenhum material do tipo da célula está atribuído à emissora
// nessa campanha. Segue §4.7 do design.md — não é só um aviso passivo, é uma
// rota guiada pra resolver o bloqueio.
function NoMaterialsState({ materialType, station, onCancel }) {
  return (
    <div style={{
      display: 'flex', gap: 14, alignItems: 'flex-start',
      padding: '16px 18px',
      background: '#fffbeb',
      border: '1px solid #fcd34d',
      borderRadius: 'var(--radius-lg)',
    }}>
      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        width: 36, height: 36, flexShrink: 0,
        borderRadius: 'var(--radius-md)',
        background: '#fde68a', color: '#92400e',
      }}>
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <circle cx="12" cy="12" r="10" /><path d="M12 8v4M12 16h.01" />
        </svg>
      </div>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{
          fontSize: 13.5, fontWeight: 700, color: '#78350f',
          fontFamily: 'var(--font-heading)', marginBottom: 4,
        }}>
          Sem materiais elegíveis
        </div>
        <div style={{ fontSize: 12, color: '#92400e', lineHeight: 1.5, marginBottom: 10 }}>
          Nenhum material do tipo <strong>{materialType?.name ?? '—'}</strong> está
          vinculado à emissora <strong>{station?.name ?? '—'}</strong> nessa
          campanha. Vincule em <em>/campaigns → Editar → Materiais</em> antes de
          inserir manualmente.
        </div>
        <GhostButton type="button" onClick={onCancel}>
          Fechar
        </GhostButton>
      </div>
    </div>
  )
}

function SummaryStat({ label, value, variant, prefix }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: 4 }}>
      <span style={{ fontSize: 10, color: '#64748b', textTransform: 'uppercase', fontWeight: 600 }}>{label}</span>
      <BadgePill variant={variant} value={value} prefix={prefix ?? ''} />
    </div>
  )
}

function DetectionsList({ grouped, materialType, activePlayerId, evidenceBlobUrls, loadingId, onPlay, onPause, onDownload }) {
  // Material type drives the colored left border + chip on every row. The row
  // title is the *individual* material name (commercial_name from the
  // detection record) so the user sees exactly which cut played even though
  // the cell is keyed by type.
  const typeColor = materialType?.color ?? '#94a3b8'
  const typeName  = materialType?.name  ?? 'Sem tipo'

  return (
    <div>
      {Object.entries(grouped).map(([cat, list]) => {
        if (list.length === 0) return null
        const { label, variant } = CATEGORY_LABEL[cat]
        return (
          <div key={cat} style={{ marginBottom: 16 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
              <BadgePill variant={variant} value={list.length} />
              <strong style={{ fontSize: 13 }}>{label}</strong>
            </div>
            <ul style={{ listStyle: 'none', padding: 0, margin: 0, fontSize: 12 }}>
              {list.map(d => {
                const isPlaying = activePlayerId === d.id
                const isLoadingThis = loadingId === d.id
                // Show audio controls for any status that has a chance of having audio.
                // Backend returns 404 if evidence isn't actually available — handled in handlePlay's catch.
                const hasEvidence = d.evidence_status === 'available' ||
                                    d.evidence_status === 'pending' ||
                                    d.evidence_status === 'generating' ||
                                    !d.evidence_status  // legacy detections may have no status
                const evidenceLabel = d.evidence_status === 'pending' ? 'processando…'
                  : d.evidence_status === 'generating' ? 'gerando…'
                  : d.evidence_status === 'missing' ? 'sem áudio'
                  : d.evidence_status === 'failed' ? 'falhou'
                  : null
                return (
                  <li key={d.id} style={{
                    padding: '8px 10px 8px 12px', background: '#fafbfc',
                    borderRadius: 6, marginBottom: 4,
                    borderLeft: `3px solid ${typeColor}`,
                    display: 'flex', flexDirection: 'column', gap: 6,
                  }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                      <span style={{ color: typeColor, fontWeight: 600, fontSize: 12 }}>
                        {d.commercial_name || 'Material sem título'}
                      </span>
                      <span style={{
                        fontSize: 10, fontWeight: 600, textTransform: 'uppercase',
                        letterSpacing: '0.04em',
                        color: typeColor,
                        background: `color-mix(in srgb, ${typeColor} 12%, transparent)`,
                        padding: '2px 7px', borderRadius: 10,
                      }}>
                        {typeName}
                      </span>
                      <Link
                        to={`/detections/${d.id}`}
                        style={{
                          marginLeft: 'auto', fontSize: 11, color: '#E81E75',
                          fontWeight: 600, textDecoration: 'none',
                        }}
                        title="Abrir detalhes da veiculação"
                      >
                        Detalhes →
                      </Link>
                    </div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 10, justifyContent: 'space-between' }}>
                      <span style={{ fontFamily: 'monospace', fontWeight: 600 }}>{fmtTime(d.detected_at)}</span>
                      <span style={{ color: '#64748b', flex: 1, marginLeft: 12 }}>
                        conf {(d.confidence * 100).toFixed(0)}% · hash {d.hash_count}
                      </span>
                      <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
                        {hasEvidence && (
                          <>
                            <AudioPlayer
                              src={evidenceBlobUrls[d.id] || ''}
                              isPlaying={isPlaying}
                              onPlay={() => onPlay(d.id)}
                              onPause={onPause}
                            />
                            <button
                              type="button"
                              className="day-detail-download"
                              onClick={() => onDownload(d.id)}
                              title="Baixar áudio"
                              aria-label="Baixar evidência de áudio"
                              disabled={isLoadingThis}
                            >
                              <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
                                <path d="M8 2v8M5 7l3 3 3-3" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
                                <path d="M2 12h12" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
                              </svg>
                            </button>
                          </>
                        )}
                        {!hasEvidence && (
                          <span style={{ fontSize: 11, color: '#94a3b8' }}>
                            {evidenceLabel || 'indisponível'}
                          </span>
                        )}
                        {hasEvidence && evidenceLabel && (
                          <span style={{ fontSize: 10, color: '#94a3b8', marginLeft: 6 }}>
                            {evidenceLabel}
                          </span>
                        )}
                      </div>
                    </div>
                  </li>
                )
              })}
            </ul>
          </div>
        )
      })}
    </div>
  )
}
