import { useState, useMemo, useEffect, useRef } from 'react'
import {
  useCampaignMaterials, useMaterials, useMaterialTypes,
  useLinkCampaignMaterial, useUnlinkCampaignMaterial, useUploadMaterial,
  useUpdateMaterialTypeId, useUpdateMaterialScript, useUpdateCampaignMaterialStations,
} from '../../api/hooks'
import api from '../../api/client'
import StationAvatar from '../../components/StationAvatar'
import { useConfirm } from '../../components/ConfirmModal'
import SimilarityWarningModal from '../../components/SimilarityWarningModal'
import SimilarityHeadsUp from '../../components/SimilarityHeadsUp'

/**
 * Step 3 of the wizard: link/upload materials to the campaign.
 *
 * Props:
 *  - campaignId: uuid
 *  - clientId: uuid (campaign.client_id)
 *  - materialsById: Record<uuid, Material> (hydrated by parent)
 *  - campaignStations: Array<station> (used as default for new links)
 */
export default function MaterialsStep({ campaignId, clientId, materialsById = {}, campaignStations, onSkip }) {
  const { data: cmpMats = [] } = useCampaignMaterials(campaignId)
  const { data: materialTypes = [] } = useMaterialTypes()
  const { data: libMats = [] } = useMaterials(clientId)
  const unlink = useUnlinkCampaignMaterial()
  const updateType = useUpdateMaterialTypeId()
  const updateScript = useUpdateMaterialScript()
  const updateStations = useUpdateCampaignMaterialStations()
  const confirm = useConfirm()
  const [showAdd, setShowAdd] = useState(false)
  // Which material card is currently in stations-editing mode (only one open at a time).
  const [editingStationsFor, setEditingStationsFor] = useState(null)
  // Same idea for the script editor — separate state so opening the script
  // panel doesn't collapse the stations panel a user might already have open.
  const [editingScriptFor, setEditingScriptFor] = useState(null)

  // Fallback decision modal: if the operator reloaded (or otherwise bypassed
  // the upload-time blocker), surface the same blocking modal for any
  // material that still has an unresolved ≥50% similarity match. We re-fire
  // until they decide — same hard rule as during upload.
  const [fallbackDecision, setFallbackDecision] = useState(null)
  // fallbackDecision shape: { newMaterial, similarMaterial } | null

  useEffect(() => {
    if (fallbackDecision) return  // one at a time
    for (const link of cmpMats) {
      const mat = materialsById[link.material_id]
      if (!mat) continue
      if (mat.similarity_check_status !== 'ready') continue
      if (mat.similarity_acknowledged_at) continue
      if (mat.similarity_score == null || mat.similarity_score < 0.50) continue
      const sim = materialsById[mat.most_similar_material_id]
      if (!sim) continue
      setFallbackDecision({ newMaterial: mat, similarMaterial: sim })
      return
    }
  }, [cmpMats, materialsById, fallbackDecision])

  const typeById = Object.fromEntries(materialTypes.map(t => [t.id, t]))

  async function handleUnlink(materialId, title) {
    const ok = await confirm(`Desvincular "${title}" desta campanha?`)
    if (!ok) return
    unlink.mutate({ campaignId, materialId })
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 28 }}>
      {/* Section title + primary action */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 16, flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6, maxWidth: 720 }}>
          <h2 style={{
            margin: 0, fontFamily: 'var(--font-heading)', fontWeight: 700,
            fontSize: 24, color: 'var(--c-text)', letterSpacing: '-0.01em',
          }}>
            Quais áudios entram na campanha?
          </h2>
          <p style={{ margin: 0, color: 'var(--c-text-2)', fontSize: 13, lineHeight: 1.55 }}>
            Vincule materiais da biblioteca do cliente ou faça upload de novos.
            Cada material gera fingerprint próprio e passa a ser monitorado em
            todas as emissoras selecionadas.
          </p>
        </div>
        <button
          onClick={() => setShowAdd(true)}
          style={{
            padding: '10px 16px', borderRadius: 'var(--radius-md)',
            background: 'var(--c-action)', color: '#fff', border: 0,
            cursor: 'pointer', fontSize: 13, fontWeight: 700,
            fontFamily: 'var(--font-heading)',
            display: 'flex', alignItems: 'center', gap: 6,
            boxShadow: 'var(--shadow-sm)',
            transition: 'all 150ms cubic-bezier(0.16,1,0.3,1)',
            whiteSpace: 'nowrap',
          }}
          onMouseEnter={e => {
            e.currentTarget.style.transform = 'translateY(-1px)'
            e.currentTarget.style.boxShadow = 'var(--shadow-md)'
          }}
          onMouseLeave={e => {
            e.currentTarget.style.transform = 'translateY(0)'
            e.currentTarget.style.boxShadow = 'var(--shadow-sm)'
          }}
        >
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <path d="M8 3v10M3 8h10" />
          </svg>
          Adicionar material
        </button>
      </div>

      {/* Counter bar */}
      {cmpMats.length > 0 && (() => {
        const noTypeCount = cmpMats.filter(cm => {
          const mat = materialsById[cm.material_id]
          return mat && !mat.type_id
        }).length
        return (
          <div style={{
            display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12,
            padding: '12px 16px',
            background: noTypeCount > 0 ? '#fef9c3' : 'var(--c-action-light, rgba(232,30,117,0.06))',
            border: `1px solid ${noTypeCount > 0 ? '#fde047' : 'var(--c-action-light, rgba(232,30,117,0.12))'}`,
            borderRadius: 'var(--radius-md)',
          }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
              <span style={{
                padding: '4px 10px', borderRadius: 'var(--radius-full)',
                background: noTypeCount > 0 ? '#ca8a04' : 'var(--c-action)', color: '#fff',
                fontSize: 11, fontWeight: 700, fontFamily: 'var(--font-heading)',
              }}>
                {cmpMats.length}
              </span>
              <span style={{ fontSize: 13, color: 'var(--c-text)', fontWeight: 600 }}>
                {cmpMats.length === 1 ? 'material vinculado' : 'materiais vinculados'} à campanha
              </span>
            </div>
            {noTypeCount > 0 ? (
              <span style={{ fontSize: 11, color: '#a16207', fontWeight: 600 }}>
                ⚠ {noTypeCount} sem tipo — defina o tipo pra poder distribuir
              </span>
            ) : (
              <span style={{ fontSize: 11, color: 'var(--c-text-3)' }}>
                Distribuição é por tipo. Materiais sem tipo não entram nas regras.
              </span>
            )}
          </div>
        )
      })()}

      {/* List */}
      {cmpMats.length === 0 ? (
        <EmptyState onAdd={() => setShowAdd(true)} onSkip={onSkip} />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          {cmpMats.map(link => {
            const mat = materialsById[link.material_id]
            if (!mat) return null
            const type = mat.type_id ? typeById[mat.type_id] : null
            const isEditing = editingStationsFor === link.material_id
            const isEditingScript = editingScriptFor === link.material_id
            return (
              <MaterialCard
                key={link.material_id}
                material={mat}
                link={link}
                type={type}
                allTypes={materialTypes}
                campaignStations={campaignStations}
                isEditingStations={isEditing}
                onToggleStationsEdit={() =>
                  setEditingStationsFor(isEditing ? null : link.material_id)}
                isEditingScript={isEditingScript}
                onToggleScriptEdit={() =>
                  setEditingScriptFor(isEditingScript ? null : link.material_id)}
                onSaveScript={(script) => {
                  updateScript.mutate(
                    { id: mat.id, script },
                    { onSuccess: () => setEditingScriptFor(null) },
                  )
                }}
                scriptSaving={updateScript.isPending}
                onTypeChange={(typeId) => updateType.mutate({ id: mat.id, type_id: typeId })}
                onSaveStations={(target_stations) => {
                  updateStations.mutate({
                    campaignId, materialId: link.material_id, target_stations,
                  })
                }}
                onUnlink={() => handleUnlink(mat.id, mat.title)}
              />
            )
          })}
        </div>
      )}

      {showAdd && (
        <AddMaterialPanel
          onClose={() => setShowAdd(false)}
          campaignId={campaignId}
          clientId={clientId}
          libraryMaterials={libMats}
          alreadyLinkedIds={new Set(cmpMats.map(l => l.material_id))}
          campaignStations={campaignStations}
          materialTypes={materialTypes}
        />
      )}

      {fallbackDecision && (
        <SimilarityWarningModal
          newMaterial={fallbackDecision.newMaterial}
          similarMaterial={fallbackDecision.similarMaterial}
          onKept={() => setFallbackDecision(null)}
          onRemoved={() => setFallbackDecision(null)}
        />
      )}
    </div>
  )
}

function fmtDuration(secs) {
  if (secs == null) return '—'
  return `${secs.toFixed(1)}s`
}

function fingerprintBadge(status) {
  const map = {
    ready:      { label: 'Pronto',    bg: '#dcfce7', fg: 'var(--c-success)', dot: 'var(--c-success)' },
    generating: { label: 'Gerando',   bg: '#fef9c3', fg: '#a16207',          dot: '#ca8a04' },
    pending:    { label: 'Pendente',  bg: 'var(--c-surface-2)', fg: 'var(--c-text-2)', dot: 'var(--c-text-3)' },
    failed:     { label: 'Falhou',    bg: '#fee2e2', fg: 'var(--c-danger)',  dot: 'var(--c-danger)' },
  }
  return map[status] ?? map.pending
}

function MaterialCard({
  material, link, type, allTypes, campaignStations,
  isEditingStations, onToggleStationsEdit,
  isEditingScript, onToggleScriptEdit, onSaveScript, scriptSaving,
  onTypeChange, onSaveStations, onUnlink,
}) {
  // Audio playback / download. Same pattern as CampaignsPage commercials row:
  // <audio> can't carry the JWT, so we fetch a blob via the authed axios
  // client, build an object URL, and feed THAT to the player and download
  // anchor. Blob URL is cached for the row's lifetime (revoked on unmount).
  const [audioBlobUrl, setAudioBlobUrl] = useState(null)
  const [playing, setPlaying] = useState(false)
  const [audioLoading, setAudioLoading] = useState(false)
  const [downloadLoading, setDownloadLoading] = useState(false)
  const audioRef = useRef(null)

  useEffect(() => () => {
    if (audioBlobUrl) URL.revokeObjectURL(audioBlobUrl)
  }, [audioBlobUrl])

  async function fetchAudioBlob() {
    if (audioBlobUrl) return audioBlobUrl
    const resp = await api.get(`/materials/${material.id}/audio`, { responseType: 'blob' })
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
      const ext = material.master_storage_path?.split('.').pop() ?? 'mp3'
      const a = document.createElement('a')
      a.href = url
      a.download = `${material.title}.${ext}`
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

  const fp = fingerprintBadge(material.fingerprint_status)
  const missingType = !type
  // When the material has no type, the left border turns amber to flag the
  // blocker for advancing the wizard. Otherwise it carries the type's color.
  const typeColor = type?.color ?? '#ca8a04'

  const totalStations = campaignStations.length
  const linkedCount   = link.target_stations.length
  const allLinked     = linkedCount === totalStations && totalStations > 0
  const noneLinked    = linkedCount === 0

  // Visual signal for the stations chip — green when all, amber when partial,
  // red when zero (material won't be detected anywhere).
  const stationsChip = noneLinked
    ? { bg: '#fee2e2', fg: 'var(--c-danger)',  label: 'sem emissora — não será detectado' }
    : allLinked
    ? { bg: '#dcfce7', fg: 'var(--c-success)', label: `em todas (${totalStations})` }
    : { bg: '#fef9c3', fg: '#a16207',          label: `${linkedCount} de ${totalStations} emissoras` }

  return (
    <div
      style={{
        background: 'var(--c-surface)',
        border: '1px solid var(--c-border)',
        borderLeft: `4px solid ${typeColor}`,
        borderRadius: 'var(--radius-md)',
        transition: 'all 150ms cubic-bezier(0.16,1,0.3,1)',
        overflow: 'hidden',
      }}
    >
      {/* ── Header row ── */}
      <div
        style={{
          padding: '14px 16px',
          display: 'flex', alignItems: 'center', gap: 16,
          transition: 'all 150ms cubic-bezier(0.16,1,0.3,1)',
        }}
      >
        {/* Icon block */}
        <div style={{
          width: 44, height: 44, borderRadius: 'var(--radius-md)',
          background: `color-mix(in srgb, ${typeColor} 12%, transparent)`,
          color: typeColor,
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          flexShrink: 0,
        }}>
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
            <path d="M9 18V5l12-2v13" />
            <circle cx="6" cy="18" r="3" />
            <circle cx="18" cy="16" r="3" />
          </svg>
        </div>

        {/* Title + meta */}
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{
            fontWeight: 700, fontSize: 14, color: 'var(--c-text)',
            fontFamily: 'var(--font-heading)',
            whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
          }}>
            {material.title}
          </div>
          <div style={{
            marginTop: 6, display: 'flex', alignItems: 'center', gap: 8,
            fontSize: 11, color: 'var(--c-text-2)', flexWrap: 'wrap',
          }}>
            <span style={{ fontWeight: 600, color: 'var(--c-text)' }}>
              {fmtDuration(material.duration_seconds)}
            </span>
            <span style={{ color: 'var(--c-text-3)' }}>·</span>
            <span style={{
              padding: '2px 8px', borderRadius: 'var(--radius-full)',
              background: fp.bg, color: fp.fg,
              fontSize: 10, fontWeight: 700, letterSpacing: '0.03em',
              display: 'inline-flex', alignItems: 'center', gap: 5,
            }}>
              <span style={{ width: 5, height: 5, borderRadius: '50%', background: fp.dot }} />
              {fp.label}
            </span>
            {missingType && (
              <>
                <span style={{ color: 'var(--c-text-3)' }}>·</span>
                <span style={{
                  padding: '2px 8px', borderRadius: 'var(--radius-full)',
                  background: '#fef3c7', color: '#a16207',
                  fontSize: 10, fontWeight: 700, letterSpacing: '0.03em',
                  display: 'inline-flex', alignItems: 'center', gap: 5,
                }}>
                  ⚠ defina o tipo pra poder distribuir
                </span>
              </>
            )}
          </div>
        </div>

        {/* Stations chip — clickable to expand the editor */}
        <button
          onClick={onToggleStationsEdit}
          title="Editar emissoras vinculadas"
          style={{
            display: 'flex', alignItems: 'center', gap: 7,
            padding: '7px 12px', borderRadius: 'var(--radius-full)',
            background: stationsChip.bg, color: stationsChip.fg,
            border: 0, cursor: 'pointer',
            fontSize: 11, fontWeight: 700,
            fontFamily: 'var(--font-heading)',
            whiteSpace: 'nowrap', maxWidth: 240,
            transition: 'all 100ms',
          }}
          onMouseEnter={e => { e.currentTarget.style.transform = 'translateY(-1px)' }}
          onMouseLeave={e => { e.currentTarget.style.transform = 'translateY(0)' }}
        >
          <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
            <rect x="2" y="6" width="12" height="7" rx="1.5" />
            <path d="M5 4.5l5-2.5" />
            <circle cx="11" cy="9.5" r="1.2" />
          </svg>
          {stationsChip.label}
          <svg
            width="11" height="11" viewBox="0 0 16 16" fill="none" stroke="currentColor"
            strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
            style={{
              transition: 'transform 200ms',
              transform: isEditingStations ? 'rotate(180deg)' : 'rotate(0deg)',
            }}
          >
            <path d="M4 6l4 4 4-4" />
          </svg>
        </button>

        {/* Type select */}
        <select
          value={material.type_id ?? ''}
          onChange={e => onTypeChange(e.target.value || null)}
          className="input"
          style={{ width: 150, fontSize: 12 }}
          title="Tipo do material"
        >
          <option value="">Sem tipo</option>
          {allTypes.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
        </select>

        {/* Play */}
        <IconActionBtn
          onClick={togglePlay}
          disabled={audioLoading}
          title={playing ? 'Pausar' : 'Ouvir material'}
          aria-label={playing ? 'Pausar' : 'Ouvir material'}
          activeColor="var(--c-action)"
          isActive={playing}
        >
          {audioLoading ? (
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" style={{
              animation: 'spin 0.8s linear infinite',
            }}>
              <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2.5" strokeOpacity="0.25" />
              <path d="M21 12a9 9 0 0 1-9 9" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
            </svg>
          ) : playing ? (
            <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor">
              <rect x="3.5" y="3" width="3" height="10" rx="0.5" />
              <rect x="9.5" y="3" width="3" height="10" rx="0.5" />
            </svg>
          ) : (
            <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor">
              <path d="M4.5 2.5v11l9-5.5z" />
            </svg>
          )}
        </IconActionBtn>

        {/* Download */}
        <IconActionBtn
          onClick={handleDownload}
          disabled={downloadLoading}
          title="Baixar áudio"
          aria-label="Baixar áudio"
        >
          {downloadLoading ? (
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" style={{
              animation: 'spin 0.8s linear infinite',
            }}>
              <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2.5" strokeOpacity="0.25" />
              <path d="M21 12a9 9 0 0 1-9 9" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
            </svg>
          ) : (
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
              <path d="M8 2v8M4.5 7L8 10.5 11.5 7" />
              <path d="M2.5 12.5h11" />
            </svg>
          )}
        </IconActionBtn>

        {/* Script editor toggle. Subtle when empty, action-colored dot when
            the material already has a script — so operators see at a glance
            which materials have copy registered without expanding each card. */}
        <IconActionBtn
          onClick={onToggleScriptEdit}
          title={material.script ? 'Editar texto do comercial' : 'Adicionar texto do comercial'}
          aria-label="Texto do comercial"
          activeColor="var(--c-action)"
          isActive={isEditingScript}
        >
          <span style={{ position: 'relative', display: 'inline-flex', alignItems: 'center', justifyContent: 'center' }}>
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
              <path d="M3 3h10v10H3z" />
              <path d="M5.5 6h5M5.5 8.5h5M5.5 11h3" />
            </svg>
            {material.script && !isEditingScript && (
              <span
                aria-hidden="true"
                style={{
                  position: 'absolute', top: -3, right: -3,
                  width: 7, height: 7, borderRadius: '50%',
                  background: 'var(--c-action)',
                  boxShadow: '0 0 0 2px var(--c-surface)',
                }}
              />
            )}
          </span>
        </IconActionBtn>

        {/* Unlink */}
        <button
          onClick={onUnlink}
          title="Desvincular da campanha"
          aria-label="Desvincular material"
          style={{
            width: 32, height: 32, borderRadius: 'var(--radius-md)',
            background: 'transparent', border: '1px solid var(--c-border)',
            color: 'var(--c-text-3)', cursor: 'pointer',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            flexShrink: 0,
            transition: 'all 100ms',
          }}
          onMouseEnter={e => {
            e.currentTarget.style.background = '#fee2e2'
            e.currentTarget.style.borderColor = '#fecaca'
            e.currentTarget.style.color = 'var(--c-danger)'
          }}
          onMouseLeave={e => {
            e.currentTarget.style.background = 'transparent'
            e.currentTarget.style.borderColor = 'var(--c-border)'
            e.currentTarget.style.color = 'var(--c-text-3)'
          }}
        >
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
            <path d="M3 5h10M6 5V3.5h4V5M5 5l1 9h4l1-9" />
          </svg>
        </button>
      </div>

      {/* Hidden audio element driven by `playing`. Mounted always (so the
          ref is stable across renders), but only ticks audio when blob is
          loaded and playing=true (effect above). */}
      <audio
        ref={audioRef}
        src={audioBlobUrl ?? undefined}
        onEnded={() => setPlaying(false)}
        style={{ display: 'none' }}
      />

      {/* ── Expandable stations editor ── */}
      {isEditingStations && (
        <StationsInlineEditor
          campaignStations={campaignStations}
          selectedIds={link.target_stations}
          onSave={onSaveStations}
          onClose={onToggleStationsEdit}
        />
      )}

      {/* ── Expandable script editor ── */}
      {isEditingScript && (
        <ScriptInlineEditor
          initial={material.script ?? ''}
          saving={scriptSaving}
          onSave={onSaveScript}
          onClose={onToggleScriptEdit}
        />
      )}

      <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>
    </div>
  )
}

/**
 * Inline expandable editor for materials.script. Mirrors the visual shape of
 * StationsInlineEditor so both expanding panels feel like the same family.
 * "Limpar" submits an empty string — server normalizes to NULL.
 */
function ScriptInlineEditor({ initial, saving, onSave, onClose }) {
  const [draft, setDraft] = useState(initial)
  const dirty = draft.trim() !== (initial ?? '').trim()
  const hasContent = draft.trim().length > 0

  return (
    <div style={{
      borderTop: '1px solid var(--c-border)',
      background: 'var(--c-bg)',
      padding: '16px 18px',
      animation: 'wizard-expand 200ms cubic-bezier(0.16,1,0.3,1)',
      display: 'flex', flexDirection: 'column', gap: 10,
    }}>
      <style>{`
        @keyframes wizard-expand {
          from { opacity: 0; transform: translateY(-4px); }
          to { opacity: 1; transform: translateY(0); }
        }
      `}</style>

      <div style={{
        display: 'flex', justifyContent: 'space-between', alignItems: 'baseline',
        gap: 12,
      }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <span style={{
            fontSize: 10.5, fontWeight: 700, letterSpacing: '0.06em',
            textTransform: 'uppercase', color: 'var(--c-text-3)',
            fontFamily: 'var(--font-heading)',
          }}>
            Texto do comercial
          </span>
          <span style={{ fontSize: 11.5, color: 'var(--c-text-2)', lineHeight: 1.5 }}>
            Roteiro / copy falado. Aparece em /detecções quando esse material veicular.
            <span style={{ color: 'var(--c-text-3)' }}> Campo opcional.</span>
          </span>
        </div>
        <span style={{
          fontSize: 10, color: 'var(--c-text-3)',
          fontVariantNumeric: 'tabular-nums',
          flexShrink: 0,
        }}>
          {draft.length} {draft.length === 1 ? 'caractere' : 'caracteres'}
        </span>
      </div>

      <textarea
        className="input"
        value={draft}
        onChange={e => setDraft(e.target.value)}
        disabled={saving}
        rows={5}
        autoFocus
        placeholder="Ex.: A nova promoção da Acme chegou. Aproveite descontos de até 30% nesta semana. Acme — sua escolha certa."
        style={{
          fontSize: 13, lineHeight: 1.55, resize: 'vertical',
          minHeight: 96, fontFamily: 'var(--font-body)',
        }}
      />

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 12 }}>
        <button
          type="button"
          onClick={() => setDraft('')}
          disabled={saving || !hasContent}
          style={{
            background: 'transparent', border: 0,
            color: hasContent ? 'var(--c-text-2)' : 'var(--c-text-3)',
            fontSize: 11.5, fontWeight: 600,
            cursor: saving || !hasContent ? 'not-allowed' : 'pointer',
            padding: '4px 0',
          }}
        >
          Limpar
        </button>
        <div style={{ display: 'flex', gap: 8 }}>
          <button
            type="button"
            onClick={onClose}
            disabled={saving}
            style={{
              padding: '8px 14px', borderRadius: 'var(--radius-md)',
              background: 'transparent', border: '1px solid var(--c-border)',
              color: 'var(--c-text-2)', fontSize: 12, fontWeight: 600,
              cursor: saving ? 'not-allowed' : 'pointer',
              fontFamily: 'var(--font-body)',
            }}
          >
            Cancelar
          </button>
          <button
            type="button"
            onClick={() => onSave(draft.trim())}
            disabled={saving || !dirty}
            style={{
              minWidth: 96,
              padding: '8px 14px', borderRadius: 'var(--radius-md)',
              background: dirty && !saving ? 'var(--c-action)' : 'var(--c-surface-2)',
              border: `1px solid ${dirty && !saving ? 'var(--c-action)' : 'var(--c-border)'}`,
              color: dirty && !saving ? '#fff' : 'var(--c-text-3)',
              fontSize: 12, fontWeight: 700,
              cursor: saving || !dirty ? 'not-allowed' : 'pointer',
              fontFamily: 'var(--font-body)',
              transition: 'all 120ms',
            }}
          >
            {saving ? 'Salvando…' : hasContent ? 'Salvar' : 'Remover'}
          </button>
        </div>
      </div>
    </div>
  )
}

/**
 * Round-rect 32x32 icon button used for the inline material-row actions
 * (play / download). Mirrors the visual weight of the trash button next to
 * it so the three form a coherent action cluster.
 */
function IconActionBtn({ onClick, disabled, title, ariaLabel, isActive, activeColor, children }) {
  const baseColor = isActive ? (activeColor ?? 'var(--c-action)') : 'var(--c-text-3)'
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      aria-label={ariaLabel ?? title}
      style={{
        width: 32, height: 32, borderRadius: 'var(--radius-md)',
        background: isActive ? 'color-mix(in srgb, var(--c-action) 10%, transparent)' : 'transparent',
        border: `1px solid ${isActive ? 'color-mix(in srgb, var(--c-action) 35%, transparent)' : 'var(--c-border)'}`,
        color: baseColor,
        cursor: disabled ? 'not-allowed' : 'pointer',
        opacity: disabled ? 0.5 : 1,
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        flexShrink: 0,
        transition: 'all 100ms',
      }}
      onMouseEnter={e => {
        if (disabled || isActive) return
        e.currentTarget.style.borderColor = 'var(--c-action-300, #F472B6)'
        e.currentTarget.style.color = 'var(--c-text-2)'
      }}
      onMouseLeave={e => {
        if (isActive) return
        e.currentTarget.style.borderColor = 'var(--c-border)'
        e.currentTarget.style.color = baseColor
      }}
    >
      {children}
    </button>
  )
}

/**
 * Inline expandable editor that lets the user toggle which campaign stations
 * are linked to a given material. Stations are grouped by UF for scannability,
 * and changes save on demand (Salvar button) — not on every checkbox change,
 * to avoid spamming the backend.
 */
function StationsInlineEditor({ campaignStations, selectedIds, onSave, onClose }) {
  const [draft, setDraft] = useState(() => new Set(selectedIds))
  const [search, setSearch] = useState('')

  const grouped = useMemo(() => {
    const m = new Map()
    const q = search.trim().toLowerCase()
    for (const st of campaignStations) {
      if (q) {
        const hay = `${st.name ?? ''} ${st.city ?? ''} ${st.state ?? ''} ${st.frequency_mhz ?? ''}`.toLowerCase()
        if (!hay.includes(q)) continue
      }
      const key = st.state || '—'
      if (!m.has(key)) m.set(key, [])
      m.get(key).push(st)
    }
    return [...m.entries()].sort((a, b) => a[0].localeCompare(b[0]))
  }, [campaignStations, search])

  function toggle(id) {
    setDraft(d => {
      const next = new Set(d)
      if (next.has(id)) next.delete(id); else next.add(id)
      return next
    })
  }
  function toggleGroup(stations, allOn) {
    setDraft(d => {
      const next = new Set(d)
      for (const s of stations) {
        if (allOn) next.delete(s.id)
        else next.add(s.id)
      }
      return next
    })
  }

  const draftCount = draft.size
  const dirty = draftCount !== selectedIds.length ||
    [...draft].some(id => !selectedIds.includes(id))

  function selectAll() { setDraft(new Set(campaignStations.map(s => s.id))) }
  function clearAll() { setDraft(new Set()) }

  return (
    <div style={{
      borderTop: '1px solid var(--c-border)',
      background: 'var(--c-bg)',
      padding: '14px 16px 16px',
      display: 'flex', flexDirection: 'column', gap: 12,
    }}>
      {/* Toolbar */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap',
      }}>
        <div style={{
          display: 'flex', alignItems: 'center', gap: 7,
          padding: '4px 10px', borderRadius: 'var(--radius-full)',
          background: 'var(--c-surface)', border: '1px solid var(--c-border)',
          fontSize: 11, fontWeight: 700, color: 'var(--c-text)',
          fontFamily: 'var(--font-heading)',
        }}>
          <span style={{ color: 'var(--c-action)' }}>{draftCount}</span>
          <span style={{ color: 'var(--c-text-3)', fontWeight: 500 }}>
            / {campaignStations.length} selecionadas
          </span>
        </div>

        <div style={{ display: 'flex', gap: 6 }}>
          <MiniBtn onClick={selectAll}>Todas</MiniBtn>
          <MiniBtn onClick={clearAll} variant="ghost">Limpar</MiniBtn>
        </div>

        <div style={{
          flex: 1, minWidth: 160, display: 'flex', alignItems: 'center', gap: 6,
          padding: '5px 10px', borderRadius: 'var(--radius-md)',
          background: 'var(--c-surface)', border: '1px solid var(--c-border)',
        }}>
          <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="var(--c-text-3)" strokeWidth="1.75" strokeLinecap="round">
            <circle cx="7" cy="7" r="5" /><path d="M11 11l3 3" />
          </svg>
          <input
            value={search}
            onChange={e => setSearch(e.target.value)}
            placeholder="Buscar emissora, cidade, UF…"
            style={{
              flex: 1, border: 0, outline: 'none', background: 'transparent',
              fontSize: 12, color: 'var(--c-text)',
            }}
          />
        </div>
      </div>

      {/* Grouped grid */}
      {grouped.length === 0 ? (
        <div style={{
          padding: '20px 12px', textAlign: 'center', fontSize: 12,
          color: 'var(--c-text-3)',
        }}>
          Nenhuma emissora corresponde a "{search}".
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12, maxHeight: 320, overflowY: 'auto', paddingRight: 4 }}>
          {grouped.map(([state, stations]) => {
            const allOn = stations.every(s => draft.has(s.id))
            const someOn = !allOn && stations.some(s => draft.has(s.id))
            return (
              <div key={state} style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                <div style={{
                  display: 'flex', alignItems: 'center', gap: 8,
                  fontSize: 11, color: 'var(--c-text-2)',
                  fontFamily: 'var(--font-heading)',
                }}>
                  <button
                    onClick={() => toggleGroup(stations, allOn)}
                    style={{
                      padding: '2px 8px', borderRadius: 'var(--radius-full)',
                      background: allOn ? 'var(--c-action)' : someOn ? 'var(--c-action-light, rgba(232,30,117,0.12))' : 'var(--c-surface-2)',
                      color: allOn ? '#fff' : someOn ? 'var(--c-action)' : 'var(--c-text)',
                      border: 0, cursor: 'pointer',
                      fontSize: 10, fontWeight: 700, letterSpacing: '0.06em',
                    }}
                    title={allOn ? `Desmarcar UF ${state}` : `Marcar UF ${state}`}
                  >
                    {state}
                  </button>
                  <span style={{ color: 'var(--c-text-3)' }}>
                    {stations.filter(s => draft.has(s.id)).length} / {stations.length}
                  </span>
                </div>
                <div style={{
                  display: 'grid',
                  gridTemplateColumns: 'repeat(auto-fill, minmax(240px, 1fr))',
                  gap: 6,
                }}>
                  {stations.map(s => (
                    <StationCheckbox
                      key={s.id}
                      station={s}
                      checked={draft.has(s.id)}
                      onToggle={() => toggle(s.id)}
                    />
                  ))}
                </div>
              </div>
            )
          })}
        </div>
      )}

      {/* Footer actions */}
      <div style={{
        display: 'flex', justifyContent: 'flex-end', gap: 8,
        paddingTop: 10, borderTop: '1px solid var(--c-border)',
      }}>
        <button
          onClick={onClose}
          style={{
            padding: '7px 14px', borderRadius: 'var(--radius-md)',
            background: 'transparent', color: 'var(--c-text-2)',
            border: '1px solid var(--c-border)', cursor: 'pointer',
            fontSize: 11, fontWeight: 600, fontFamily: 'var(--font-heading)',
          }}
        >
          Cancelar
        </button>
        <button
          onClick={() => {
            onSave([...draft])
            onClose()
          }}
          disabled={!dirty}
          style={{
            padding: '7px 16px', borderRadius: 'var(--radius-md)',
            background: dirty ? 'var(--c-action)' : 'var(--c-surface-2)',
            color: dirty ? '#fff' : 'var(--c-text-3)',
            border: 0, cursor: dirty ? 'pointer' : 'not-allowed',
            fontSize: 11, fontWeight: 700, fontFamily: 'var(--font-heading)',
          }}
        >
          {dirty ? `Salvar (${draftCount})` : 'Sem alterações'}
        </button>
      </div>
    </div>
  )
}

function MiniBtn({ onClick, children, variant = 'solid' }) {
  return (
    <button
      onClick={onClick}
      style={{
        padding: '4px 10px', borderRadius: 'var(--radius-md)',
        background: variant === 'ghost' ? 'transparent' : 'var(--c-surface)',
        color: variant === 'ghost' ? 'var(--c-text-2)' : 'var(--c-text)',
        border: '1px solid var(--c-border)', cursor: 'pointer',
        fontSize: 11, fontWeight: 600, fontFamily: 'var(--font-heading)',
        transition: 'all 100ms',
      }}
      onMouseEnter={e => { e.currentTarget.style.borderColor = 'var(--c-action-300, #F472B6)' }}
      onMouseLeave={e => { e.currentTarget.style.borderColor = 'var(--c-border)' }}
    >
      {children}
    </button>
  )
}

function StationCheckbox({ station, checked, onToggle }) {
  const freq = station.frequency_mhz != null ? ` ${station.frequency_mhz}` : ''
  return (
    <label
      style={{
        display: 'flex', alignItems: 'center', gap: 10,
        padding: '7px 10px', borderRadius: 'var(--radius-md)',
        background: checked ? 'var(--c-action-light, rgba(232,30,117,0.08))' : 'var(--c-surface)',
        border: `1px solid ${checked ? 'var(--c-action-300, #F472B6)' : 'var(--c-border)'}`,
        cursor: 'pointer',
        transition: 'all 100ms',
        minWidth: 0,
      }}
    >
      <input
        type="checkbox"
        checked={checked}
        onChange={onToggle}
        style={{ width: 14, height: 14, accentColor: 'var(--c-action)', flexShrink: 0 }}
      />
      <StationAvatar station={station} size={22} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{
          fontSize: 12, fontWeight: 600, color: 'var(--c-text)',
          fontFamily: 'var(--font-heading)',
          whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
        }}>
          {station.name}
        </div>
        <div style={{
          fontSize: 10, color: 'var(--c-text-3)',
          whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
        }}>
          {station.band || ''}{freq}{station.city ? ` · ${station.city}` : ''}
        </div>
      </div>
    </label>
  )
}

function EmptyState({ onAdd, onSkip }) {
  // Ghost preview of what loaded cards look like.
  return (
    <div style={{
      position: 'relative',
      background: 'var(--c-bg)',
      border: '1px dashed var(--c-border)',
      borderRadius: 'var(--radius-xl)',
      padding: '48px 32px',
      overflow: 'hidden',
    }}>
      {/* Ghost cards */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 10, opacity: 0.3, pointerEvents: 'none' }}>
        {['#3b82f6', '#8b5cf6', '#14b8a6'].map((c, i) => (
          <div key={i} style={{
            background: 'var(--c-surface)',
            border: '1px solid var(--c-border)',
            borderLeft: `4px solid ${c}`,
            borderRadius: 'var(--radius-md)',
            padding: '14px 16px',
            display: 'flex', alignItems: 'center', gap: 14,
          }}>
            <div style={{ width: 44, height: 44, borderRadius: 'var(--radius-md)', background: 'var(--c-surface-2)' }} />
            <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 6 }}>
              <div style={{ height: 10, background: 'var(--c-surface-2)', borderRadius: 4, width: `${60 - i * 8}%` }} />
              <div style={{ height: 8, background: 'var(--c-surface-2)', borderRadius: 3, width: '35%' }} />
            </div>
            <div style={{ width: 140, height: 28, background: 'var(--c-surface-2)', borderRadius: 'var(--radius-sm)' }} />
          </div>
        ))}
      </div>

      {/* CTA overlay */}
      <div style={{
        position: 'absolute', inset: 0,
        display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
        gap: 8,
        background: 'linear-gradient(to bottom, rgba(248,250,252,0.6), rgba(248,250,252,0.98))',
      }}>
        <div style={{
          width: 56, height: 56, borderRadius: '50%',
          background: 'var(--c-surface)', boxShadow: 'var(--shadow-md)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          color: 'var(--c-action)', marginBottom: 6,
        }}>
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
            <path d="M9 18V5l12-2v13" />
            <circle cx="6" cy="18" r="3" />
            <circle cx="18" cy="16" r="3" />
          </svg>
        </div>
        <h3 style={{
          margin: 0, fontFamily: 'var(--font-heading)', fontWeight: 700,
          fontSize: 16, color: 'var(--c-text)',
        }}>
          Nenhum material ainda
        </h3>
        <p style={{
          margin: 0, color: 'var(--c-text-2)', fontSize: 13,
          textAlign: 'center', maxWidth: 360, lineHeight: 1.5,
        }}>
          Vincule áudios já cadastrados na biblioteca deste cliente ou suba
          arquivos novos (WAV, MP3, M4A).
        </p>
        <button
          onClick={onAdd}
          style={{
            marginTop: 8, padding: '10px 18px', borderRadius: 'var(--radius-md)',
            background: 'var(--c-action)', color: '#fff', border: 0,
            cursor: 'pointer', fontSize: 13, fontWeight: 700,
            fontFamily: 'var(--font-heading)',
            display: 'flex', alignItems: 'center', gap: 6,
            boxShadow: 'var(--shadow-md)',
            transition: 'all 150ms cubic-bezier(0.16,1,0.3,1)',
          }}
          onMouseEnter={e => {
            e.currentTarget.style.transform = 'translateY(-1px)'
            e.currentTarget.style.boxShadow = 'var(--shadow-lg)'
          }}
          onMouseLeave={e => {
            e.currentTarget.style.transform = 'translateY(0)'
            e.currentTarget.style.boxShadow = 'var(--shadow-md)'
          }}
        >
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <path d="M8 3v10M3 8h10" />
          </svg>
          Adicionar primeiro material
        </button>
        {onSkip && (
          <button
            type="button"
            onClick={onSkip}
            style={{
              marginTop: 12,
              padding: '6px 12px',
              background: 'transparent', border: 0,
              color: 'var(--c-text-2)',
              fontSize: 12, fontWeight: 500,
              fontFamily: 'var(--font-body)',
              cursor: 'pointer',
              textDecoration: 'underline',
              textUnderlineOffset: 3,
              textDecorationColor: 'var(--c-text-3)',
            }}
            onMouseEnter={e => { e.currentTarget.style.color = 'var(--c-text)' }}
            onMouseLeave={e => { e.currentTarget.style.color = 'var(--c-text-2)' }}
          >
            Pular por enquanto — planejar distribuição sem áudio
          </button>
        )}
      </div>
    </div>
  )
}

function AddMaterialPanel({
  onClose, campaignId, clientId, libraryMaterials, alreadyLinkedIds,
  campaignStations, materialTypes,
}) {
  const [tab, setTab] = useState('library')
  const [selectedLibIds, setSelectedLibIds] = useState(new Set())
  const [uploadQueue, setUploadQueue] = useState([])
  // busy = a submitUploads pass is in flight. While true, the drawer is
  // locked: no tab switching, no closing, no editing the queue. The lock
  // covers upload → fingerprint poll → similarity poll → decision modal →
  // link. It releases only after every queue entry settles.
  const [busy, setBusy] = useState(false)
  // pendingDecision is non-null while the blocking modal is open mid-flow.
  // The `resolve` callback unblocks submitUploads for the next entry.
  const [pendingDecision, setPendingDecision] = useState(null)
  const link = useLinkCampaignMaterial()
  const upload = useUploadMaterial()

  const available = libraryMaterials.filter(m => !alreadyLinkedIds.has(m.id))
  const defaultStationIds = campaignStations.map(s => s.id)

  function setEntryStage(key, stage, extra = {}) {
    setUploadQueue(q => q.map(e => e.key === key ? { ...e, stage, ...extra } : e))
  }

  async function linkSelected() {
    setBusy(true)
    try {
      for (const id of selectedLibIds) {
        await link.mutateAsync({ campaignId, material_id: id, target_stations: defaultStationIds })
      }
      onClose()
    } finally {
      setBusy(false)
    }
  }

  function addFiles(files) {
    const entries = Array.from(files)
      .filter(f => /\.(wav|mp3|m4a|aac|mpeg)$/i.test(f.name))
      .map(file => ({
        key: `${file.name}-${Date.now()}-${Math.random()}`,
        file,
        title: file.name.replace(/\.[^.]+$/, ''),
        typeId: '',
        // Optional spoken-copy. Persists as materials.script when non-empty.
        script: '',
        stage: 'queued',   // queued | uploading | fingerprinting | verifying | deciding | linking | done | removed | error
        errorMsg: null,
      }))
    setUploadQueue(q => [...q, ...entries])
  }

  function removeEntry(key) {
    setUploadQueue(q => q.filter(e => e.key !== key))
  }

  // pollMaterial polls GET /materials/{id} every intervalMs until cond(mat)
  // returns true, or until timeoutMs elapses (throws). cond receives the
  // fresh material payload each iteration.
  async function pollMaterial(id, cond, { intervalMs = 1500, timeoutMs = 90_000 } = {}) {
    const start = Date.now()
    while (Date.now() - start < timeoutMs) {
      const r = await api.get(`/materials/${id}`)
      if (cond(r.data)) return r.data
      await new Promise(res => setTimeout(res, intervalMs))
    }
    throw new Error('poll timeout')
  }

  async function submitUploads() {
    setBusy(true)
    try {
      for (const entry of uploadQueue) {
        if (entry.stage !== 'queued' && entry.stage !== 'error') continue

        // 1. Upload the file
        setEntryStage(entry.key, 'uploading', { errorMsg: null })
        const fd = new FormData()
        fd.append('client_id', clientId)
        fd.append('title', entry.title)
        if (entry.typeId) fd.append('type_id', entry.typeId)
        if (entry.script && entry.script.trim()) fd.append('script', entry.script.trim())
        fd.append('audio', entry.file)

        let mat
        try {
          mat = await upload.mutateAsync(fd)
        } catch (e) {
          setEntryStage(entry.key, 'error', { errorMsg: 'Falha no upload' })
          continue
        }

        // 2. Wait for fingerprint
        setEntryStage(entry.key, 'fingerprinting', { materialId: mat.id })
        let fp
        try {
          fp = await pollMaterial(mat.id,
            m => m.fingerprint_status === 'ready' || m.fingerprint_status === 'failed')
        } catch {
          setEntryStage(entry.key, 'error', { errorMsg: 'Timeout no fingerprint' })
          continue
        }
        if (fp.fingerprint_status === 'failed') {
          setEntryStage(entry.key, 'error', { errorMsg: 'Não foi possível gerar o fingerprint' })
          continue
        }

        // Material curto (<10s): fica fora da defesa shared-hash e, se existir
        // spot do MESMO cliente contendo este áudio, as tocadas são disputadas
        // entre os dois (incident-2026-07-24-pulso-milium). Aviso não-bloqueante.
        const durS = Number(fp.duration_seconds)
        if (Number.isFinite(durS) && durS > 0 && durS < 10) {
          setEntryStage(entry.key, 'verifying', {
            shortWarning: `Material curto (${durS.toFixed(1)}s): detecção menos robusta e, se houver um spot deste cliente que contenha este áudio, as veiculações podem ser atribuídas ao spot. Confirme com o suporte antes de faturar por este material.`,
          })
        }

        // 3. Wait for similarity check (ready / skipped / failed)
        setEntryStage(entry.key, 'verifying')
        let verified
        try {
          verified = await pollMaterial(mat.id,
            m => ['ready', 'skipped', 'failed'].includes(m.similarity_check_status))
        } catch {
          setEntryStage(entry.key, 'error', { errorMsg: 'Timeout na verificação' })
          continue
        }

        // 4. Surface por faixa de similaridade:
        //    ≥0.50 → modal bloqueante (manter/remover)
        //    0.25–0.50 → heads-up não-bloqueante (segue sempre)
        //    <0.25 → nada
        const score = verified.similarity_check_status === 'ready'
          ? (verified.similarity_score ?? 0) : 0
        const hasMatch = !verified.similarity_acknowledged_at && verified.most_similar_material_id
        const kind = hasMatch && score >= 0.50 ? 'blocking'
          : hasMatch && score >= 0.25 ? 'headsup'
          : null

        if (kind) {
          // Fetch the comparison target
          let similar
          try {
            const r = await api.get(`/materials/${verified.most_similar_material_id}`)
            similar = r.data
          } catch {
            // If the similar material can't be loaded, fall through to linking
            // — the warning was informational and we can't show it without it.
            similar = null
          }

          if (similar) {
            setEntryStage(entry.key, 'deciding')
            const decision = await new Promise((resolve) => {
              setPendingDecision({ kind, newMaterial: verified, similarMaterial: similar, resolve })
            })
            setPendingDecision(null)
            if (decision === 'removed') {
              setEntryStage(entry.key, 'removed')
              continue
            }
            // 'kept' (modal) ou 'continue' (heads-up) → segue pro link
          }
        }

        // 5. Link
        setEntryStage(entry.key, 'linking')
        try {
          await link.mutateAsync({
            campaignId,
            material_id: mat.id,
            target_stations: defaultStationIds,
          })
          setEntryStage(entry.key, 'done')
        } catch {
          setEntryStage(entry.key, 'error', { errorMsg: 'Falha ao vincular à campanha' })
        }
      }
    } finally {
      setBusy(false)
    }

    // Auto-close 600ms after the last entry settled, if nothing errored.
    setUploadQueue(q => {
      const allClean = q.every(e => e.stage === 'done' || e.stage === 'removed')
      if (allClean) setTimeout(onClose, 600)
      return q
    })
  }

  const pendingCount = uploadQueue.filter(e => e.stage === 'queued' || e.stage === 'error').length
  const hasErrors = uploadQueue.some(e => e.stage === 'error')

  // Show the verification panel (read-only progress view) any time there's a
  // queue entry past 'queued' — that means a flow is in progress or has
  // settled. The initial dropzone+queue editor only shows when the queue is
  // entirely fresh OR entirely cleared.
  const showVerificationView = uploadQueue.some(
    e => e.stage !== 'queued' && e.stage !== 'error',
  )

  return (
    <div
      style={{
        position: 'fixed', inset: 0,
        background: 'rgba(15,23,42,0.45)', backdropFilter: 'blur(6px)',
        zIndex: 50,
        animation: 'wizard-fade-in 200ms cubic-bezier(0.16,1,0.3,1)',
      }}
      // Backdrop click closes ONLY when nothing is in flight. While busy
      // the operator cannot escape sideways — they must finish the run.
      onClick={busy ? undefined : onClose}
    >
      <style>{`
        @keyframes wizard-fade-in { from { opacity: 0; } to { opacity: 1; } }
        @keyframes wizard-slide-in { from { transform: translateX(20px); opacity: 0; } to { transform: translateX(0); opacity: 1; } }
        @keyframes wizard-stage-pulse {
          0%, 100% { box-shadow: 0 0 0 0 color-mix(in srgb, var(--c-action) 50%, transparent); }
          50%      { box-shadow: 0 0 0 6px color-mix(in srgb, var(--c-action) 0%,  transparent); }
        }
      `}</style>
      <aside
        onClick={e => e.stopPropagation()}
        style={{
          position: 'fixed', top: 0, right: 0, bottom: 0, width: 560,
          background: 'var(--c-surface)',
          boxShadow: '-24px 0 48px -12px rgba(15,23,42,0.25)',
          display: 'flex', flexDirection: 'column',
          animation: 'wizard-slide-in 300ms cubic-bezier(0.16,1,0.3,1)',
        }}
      >
        {/* ── Header ─────────────────────────────────────────────── */}
        <div style={{
          padding: '20px 24px 18px',
          borderBottom: '1px solid var(--c-border)',
          display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start',
          gap: 12,
        }}>
          <div style={{ minWidth: 0 }}>
            <span style={{
              fontSize: 10, fontWeight: 700, letterSpacing: '0.14em',
              color: busy ? 'var(--c-text-3)' : 'var(--c-action)',
              textTransform: 'uppercase',
              fontFamily: 'var(--font-heading)',
            }}>
              {busy ? 'Processando' : 'Adicionar à campanha'}
            </span>
            <h3 style={{
              margin: '4px 0 0', fontFamily: 'var(--font-heading)', fontWeight: 700,
              fontSize: 18, color: 'var(--c-text)', lineHeight: 1.3,
            }}>
              {busy
                ? 'Subindo e verificando…'
                : tab === 'library' ? 'Vincular da biblioteca' : 'Upload de novos áudios'}
            </h3>
            {busy && (
              <p style={{
                margin: '6px 0 0', fontSize: 11.5, color: 'var(--c-text-2)',
                lineHeight: 1.5,
              }}>
                Não feche essa aba — estamos comparando o áudio com a biblioteca do cliente.
              </p>
            )}
          </div>
          <button
            onClick={busy ? undefined : onClose}
            disabled={busy}
            aria-label="Fechar"
            style={{
              width: 30, height: 30, border: 0,
              background: 'var(--c-surface-2)',
              color: busy ? 'var(--c-text-3)' : 'var(--c-text-2)',
              borderRadius: 'var(--radius-md)',
              cursor: busy ? 'not-allowed' : 'pointer',
              opacity: busy ? 0.5 : 1,
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              flexShrink: 0,
            }}
          >
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round">
              <path d="M4 4l8 8M12 4l-8 8" />
            </svg>
          </button>
        </div>

        {/* ── Tabs (hidden while verifying — single-purpose view) ─── */}
        {!showVerificationView && (
          <div style={{
            display: 'flex',
            borderBottom: '1px solid var(--c-border)',
            background: 'var(--c-bg)',
          }}>
            <TabBtn
              active={tab === 'library'}
              disabled={busy}
              onClick={() => !busy && setTab('library')}
            >
              Biblioteca <Counter>{available.length}</Counter>
            </TabBtn>
            <TabBtn
              active={tab === 'upload'}
              disabled={busy}
              onClick={() => !busy && setTab('upload')}
            >
              Upload {uploadQueue.length > 0 && <Counter>{uploadQueue.length}</Counter>}
            </TabBtn>
          </div>
        )}

        {/* ── Body ───────────────────────────────────────────────── */}
        <div style={{ flex: 1, overflowY: 'auto', padding: showVerificationView ? '20px 24px' : 22 }}>
          {showVerificationView ? (
            <VerificationPanel
              entries={uploadQueue}
              onRetryEntry={(key) => setEntryStage(key, 'queued', { errorMsg: null })}
              onRemoveEntry={removeEntry}
              busy={busy}
            />
          ) : tab === 'library' ? (
            available.length === 0 ? (
              <NoLibraryItems onSwitchTab={() => setTab('upload')} />
            ) : (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                {available.map(m => {
                  const checked = selectedLibIds.has(m.id)
                  return (
                    <label
                      key={m.id}
                      style={{
                        display: 'flex', alignItems: 'center', gap: 12,
                        padding: '10px 12px', borderRadius: 'var(--radius-md)',
                        background: checked ? 'var(--c-action-light, rgba(232,30,117,0.06))' : 'transparent',
                        border: `1px solid ${checked ? 'var(--c-action-300, #F472B6)' : 'transparent'}`,
                        cursor: busy ? 'not-allowed' : 'pointer',
                        opacity: busy ? 0.5 : 1,
                        transition: 'all 100ms',
                      }}
                    >
                      <input
                        type="checkbox"
                        checked={checked}
                        disabled={busy}
                        onChange={() => {
                          const next = new Set(selectedLibIds)
                          if (next.has(m.id)) next.delete(m.id); else next.add(m.id)
                          setSelectedLibIds(next)
                        }}
                        style={{ width: 16, height: 16, accentColor: 'var(--c-action)' }}
                      />
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{
                          fontSize: 13, fontWeight: 600, color: 'var(--c-text)',
                          whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
                        }}>
                          {m.title}
                        </div>
                        <div style={{ fontSize: 11, color: 'var(--c-text-3)', marginTop: 1 }}>
                          {fmtDuration(m.duration_seconds)} · {fingerprintBadge(m.fingerprint_status).label.toLowerCase()}
                        </div>
                      </div>
                    </label>
                  )
                })}
              </div>
            )
          ) : (
            <UploadTab
              uploadQueue={uploadQueue}
              setUploadQueue={setUploadQueue}
              addFiles={addFiles}
              materialTypes={materialTypes}
              disabled={busy}
            />
          )}
        </div>

        {/* ── Footer ─────────────────────────────────────────────── */}
        <div style={{
          padding: '14px 24px',
          borderTop: '1px solid var(--c-border)',
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          gap: 12,
          background: 'var(--c-surface)',
        }}>
          <span style={{ fontSize: 11, color: 'var(--c-text-3)' }}>
            {busy && 'Aguardando análise terminar…'}
            {!busy && hasErrors && (
              <span style={{ color: 'var(--c-danger)', fontWeight: 600 }}>
                {uploadQueue.filter(e => e.stage === 'error').length} com erro
              </span>
            )}
          </span>
          <div style={{ display: 'flex', gap: 10 }}>
            <button
              onClick={busy ? undefined : onClose}
              disabled={busy}
              style={{
                padding: '9px 16px', borderRadius: 'var(--radius-md)',
                background: 'transparent',
                color: busy ? 'var(--c-text-3)' : 'var(--c-text-2)',
                border: '1px solid var(--c-border)',
                cursor: busy ? 'not-allowed' : 'pointer',
                opacity: busy ? 0.5 : 1,
                fontSize: 12, fontWeight: 600, fontFamily: 'var(--font-heading)',
              }}
            >
              Cancelar
            </button>
            {tab === 'library' && !showVerificationView ? (
              <PrimaryButton
                onClick={linkSelected}
                disabled={busy || selectedLibIds.size === 0}
                loading={busy}
              >
                Vincular {selectedLibIds.size > 0 ? `(${selectedLibIds.size})` : ''}
              </PrimaryButton>
            ) : (
              <PrimaryButton
                onClick={submitUploads}
                disabled={busy || pendingCount === 0}
                loading={busy}
              >
                {busy
                  ? 'Verificando…'
                  : hasErrors ? 'Tentar novamente' : 'Subir e verificar'}
              </PrimaryButton>
            )}
          </div>
        </div>
      </aside>

      {/* ── Similaridade: bloqueante (≥50%) ou heads-up (25–50%) ── */}
      {pendingDecision && pendingDecision.kind === 'blocking' && (
        <SimilarityWarningModal
          newMaterial={pendingDecision.newMaterial}
          similarMaterial={pendingDecision.similarMaterial}
          onKept={() => pendingDecision.resolve('kept')}
          onRemoved={() => pendingDecision.resolve('removed')}
        />
      )}
      {pendingDecision && pendingDecision.kind === 'headsup' && (
        <SimilarityHeadsUp
          newMaterial={pendingDecision.newMaterial}
          similarMaterial={pendingDecision.similarMaterial}
          onContinue={() => pendingDecision.resolve('continue')}
        />
      )}
    </div>
  )
}

function TabBtn({ active, disabled, onClick, children }) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      style={{
        flex: 1, padding: '12px 14px', border: 0,
        borderBottom: active ? '2px solid var(--c-action)' : '2px solid transparent',
        background: 'transparent',
        cursor: disabled ? 'not-allowed' : 'pointer',
        opacity: disabled ? 0.45 : 1,
        color: active ? 'var(--c-action)' : 'var(--c-text-2)',
        fontWeight: active ? 700 : 600,
        fontSize: 12, fontFamily: 'var(--font-heading)',
        display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6,
        transition: 'all 150ms',
      }}
    >
      {children}
    </button>
  )
}

function Counter({ children }) {
  return (
    <span style={{
      padding: '1px 7px', borderRadius: 'var(--radius-full)',
      background: 'var(--c-surface-2)', color: 'var(--c-text-2)',
      fontSize: 10, fontWeight: 700,
    }}>
      {children}
    </span>
  )
}

function NoLibraryItems({ onSwitchTab }) {
  return (
    <div style={{
      padding: '32px 16px', textAlign: 'center',
      color: 'var(--c-text-2)',
    }}>
      <div style={{
        width: 56, height: 56, borderRadius: '50%',
        background: 'var(--c-surface-2)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        color: 'var(--c-text-3)', margin: '0 auto 12px',
      }}>
        <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
          <path d="M9 18V5l12-2v13" />
          <circle cx="6" cy="18" r="3" />
          <circle cx="18" cy="16" r="3" />
        </svg>
      </div>
      <div style={{
        fontFamily: 'var(--font-heading)', fontWeight: 700,
        fontSize: 14, color: 'var(--c-text)', marginBottom: 4,
      }}>
        Biblioteca do cliente vazia
      </div>
      <p style={{ fontSize: 12, lineHeight: 1.55, margin: '0 auto 14px', maxWidth: 280 }}>
        Esse cliente ainda não tem materiais cadastrados. Use a aba "Upload"
        para subir o primeiro arquivo.
      </p>
      <button
        onClick={onSwitchTab}
        style={{
          padding: '8px 14px', borderRadius: 'var(--radius-md)',
          background: 'var(--c-action)', color: '#fff', border: 0,
          cursor: 'pointer', fontSize: 12, fontWeight: 700,
          fontFamily: 'var(--font-heading)',
        }}
      >
        Ir para Upload →
      </button>
    </div>
  )
}

function UploadTab({ uploadQueue, setUploadQueue, addFiles, materialTypes, disabled }) {
  const [dragOver, setDragOver] = useState(false)

  function onDrop(e) {
    e.preventDefault()
    setDragOver(false)
    if (disabled) return
    if (e.dataTransfer.files?.length) addFiles(e.dataTransfer.files)
  }

  return (
    <div>
      <label
        onDragOver={e => { e.preventDefault(); if (!disabled) setDragOver(true) }}
        onDragLeave={() => setDragOver(false)}
        onDrop={onDrop}
        style={{
          display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
          gap: 6, padding: '28px 20px',
          background: dragOver ? 'var(--c-action-light, rgba(232,30,117,0.08))' : 'var(--c-bg)',
          border: `2px dashed ${dragOver ? 'var(--c-action)' : 'var(--c-border)'}`,
          borderRadius: 'var(--radius-lg)',
          cursor: disabled ? 'not-allowed' : 'pointer',
          opacity: disabled ? 0.55 : 1,
          transition: 'all 150ms cubic-bezier(0.16,1,0.3,1)',
          textAlign: 'center',
        }}
      >
        <div style={{
          width: 40, height: 40, borderRadius: '50%',
          background: 'var(--c-surface)', boxShadow: 'var(--shadow-sm)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          color: 'var(--c-action)',
        }}>
          <svg width="18" height="18" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
            <path d="M8 11V3M5 6l3-3 3 3" />
            <path d="M2 12v1a1 1 0 0 0 1 1h10a1 1 0 0 0 1-1v-1" />
          </svg>
        </div>
        <span style={{
          fontFamily: 'var(--font-heading)', fontWeight: 700,
          fontSize: 13, color: 'var(--c-text)',
        }}>
          Arraste arquivos aqui
        </span>
        <span style={{ fontSize: 11, color: 'var(--c-text-3)' }}>
          ou <span style={{ color: 'var(--c-action)', fontWeight: 600 }}>clique para selecionar</span>
        </span>
        <span style={{ fontSize: 10, color: 'var(--c-text-3)', marginTop: 4 }}>
          WAV · MP3 · M4A · AAC — múltiplos arquivos
        </span>
        <input
          type="file"
          multiple
          accept=".wav,.mp3,.m4a,.aac,.mpeg"
          disabled={disabled}
          onChange={e => { addFiles(e.target.files); e.target.value = '' }}
          style={{ display: 'none' }}
        />
      </label>

      {uploadQueue.length > 0 && (
        <div style={{ marginTop: 16, display: 'flex', flexDirection: 'column', gap: 8 }}>
          {uploadQueue.map(entry => {
            const editable = !disabled && entry.stage === 'queued'
            return (
              <div key={entry.key} style={{
                padding: 12, background: 'var(--c-bg)',
                border: '1px solid var(--c-border)', borderRadius: 'var(--radius-md)',
                display: 'flex', flexDirection: 'column', gap: 8,
              }}>
                <div style={{
                  display: 'flex', alignItems: 'center', gap: 8,
                  fontSize: 11, color: 'var(--c-text-3)',
                  whiteSpace: 'nowrap', overflow: 'hidden',
                }}>
                  <svg width="11" height="11" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M9 1H4a1 1 0 0 0-1 1v12a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1V5z" />
                    <polyline points="9 1 9 5 13 5" />
                  </svg>
                  <span style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>{entry.file.name}</span>
                </div>
                <input
                  className="input"
                  placeholder="Título do material"
                  value={entry.title}
                  onChange={e => setUploadQueue(q => q.map(x => x.key === entry.key ? { ...x, title: e.target.value } : x))}
                  disabled={!editable}
                />
                <select
                  className="input"
                  value={entry.typeId}
                  onChange={e => setUploadQueue(q => q.map(x => x.key === entry.key ? { ...x, typeId: e.target.value } : x))}
                  disabled={!editable}
                  style={{ fontSize: 12 }}
                >
                  <option value="">Sem tipo (opcional)</option>
                  {materialTypes.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
                </select>
                {/* Texto do comercial — opcional, vai pra materials.script.
                    Aparece em /detections/:id quando a veiculação rolar.
                    Sem limite de caracteres no banco (TEXT); a UI mostra um
                    contador discreto pra dar noção do tamanho. */}
                <div>
                  <div style={{
                    display: 'flex', justifyContent: 'space-between',
                    alignItems: 'baseline', marginBottom: 4,
                  }}>
                    <label
                      htmlFor={`script-${entry.key}`}
                      style={{
                        fontSize: 10.5, fontWeight: 700, letterSpacing: '0.04em',
                        textTransform: 'uppercase', color: 'var(--c-text-3)',
                      }}
                    >
                      Texto do comercial <span style={{ fontWeight: 500, textTransform: 'none', letterSpacing: 0, color: 'var(--c-text-3)' }}>(opcional)</span>
                    </label>
                    {entry.script?.length > 0 && (
                      <span style={{ fontSize: 10, color: 'var(--c-text-3)', fontVariantNumeric: 'tabular-nums' }}>
                        {entry.script.length}
                      </span>
                    )}
                  </div>
                  <textarea
                    id={`script-${entry.key}`}
                    className="input"
                    placeholder="Cole o roteiro / copy falado. Aparece em /detecções quando o material veicular."
                    value={entry.script}
                    onChange={e => setUploadQueue(q => q.map(x => x.key === entry.key ? { ...x, script: e.target.value } : x))}
                    disabled={!editable}
                    rows={3}
                    style={{
                      fontSize: 12, lineHeight: 1.5, resize: 'vertical',
                      minHeight: 64, fontFamily: 'var(--font-body)',
                    }}
                  />
                </div>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                  <span style={{
                    fontSize: 11, fontWeight: 600,
                    color: entry.stage === 'error' ? 'var(--c-danger)' : 'var(--c-text-2)',
                  }}>
                    {entry.stage === 'error'
                      ? `✕ ${entry.errorMsg ?? 'Erro'}`
                      : '○ Pronto pra subir'}
                  </span>
                  {editable && (
                    <button
                      type="button"
                      onClick={() => setUploadQueue(q => q.filter(x => x.key !== entry.key))}
                      style={{
                        background: 'transparent', border: 0, cursor: 'pointer',
                        color: 'var(--c-text-3)', fontSize: 11,
                        padding: 4,
                      }}
                      title="Remover da fila"
                    >
                      Remover
                    </button>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

/* ─────────────────────────────────────────────────────────────────── */

/**
 * VerificationPanel — read-only progress view rendered while submitUploads is
 * in flight (or after, if there are settled entries to inspect). Each entry
 * shows its stage as a 5-step checklist with explicit completion states.
 */
function VerificationPanel({ entries, onRetryEntry, onRemoveEntry, busy }) {
  const total = entries.length
  const settled = entries.filter(
    e => e.stage === 'done' || e.stage === 'removed' || e.stage === 'error',
  ).length

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        padding: '12px 14px',
        background: 'var(--c-bg)',
        border: '1px solid var(--c-border)',
        borderRadius: 'var(--radius-md)',
      }}>
        <div>
          <div style={{
            fontSize: 10, fontWeight: 700, letterSpacing: '0.14em',
            color: 'var(--c-text-3)', textTransform: 'uppercase',
            fontFamily: 'var(--font-heading)',
          }}>
            Progresso
          </div>
          <div style={{
            marginTop: 4, fontSize: 14, fontWeight: 700,
            fontFamily: 'var(--font-heading)', color: 'var(--c-text)',
          }}>
            {settled} de {total} {total === 1 ? 'arquivo' : 'arquivos'}
          </div>
        </div>
        <div style={{
          width: 110, height: 4,
          background: 'var(--c-surface-2)', borderRadius: 'var(--radius-full)',
          overflow: 'hidden',
        }}>
          <div style={{
            width: `${total === 0 ? 0 : Math.round((settled / total) * 100)}%`,
            height: '100%',
            background: 'var(--c-action)',
            transition: 'width 250ms cubic-bezier(0.16,1,0.3,1)',
          }} />
        </div>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        {entries.map(e => (
          <VerificationEntryCard
            key={e.key}
            entry={e}
            busy={busy}
            onRetry={() => onRetryEntry(e.key)}
            onRemove={() => onRemoveEntry(e.key)}
          />
        ))}
      </div>
    </div>
  )
}

const STAGE_ORDER = ['uploading', 'fingerprinting', 'verifying', 'deciding', 'linking']
const STAGE_LABELS = {
  uploading:      'Enviando arquivo',
  fingerprinting: 'Gerando fingerprint',
  verifying:      'Verificando similaridade',
  deciding:       'Aguardando sua decisão',
  linking:        'Vinculando à campanha',
}

function VerificationEntryCard({ entry, busy, onRetry, onRemove }) {
  const isError = entry.stage === 'error'
  const isDone = entry.stage === 'done'
  const isRemoved = entry.stage === 'removed'
  const isQueued = entry.stage === 'queued'
  const currentIdx = STAGE_ORDER.indexOf(entry.stage)

  // Border accent communicates the entry's overall state
  let borderColor = 'var(--c-border)'
  if (isError) borderColor = 'color-mix(in srgb, var(--c-danger) 50%, transparent)'
  else if (isDone) borderColor = 'color-mix(in srgb, var(--c-success) 50%, transparent)'
  else if (isRemoved) borderColor = 'color-mix(in srgb, var(--c-text-3) 35%, transparent)'
  else if (currentIdx >= 0) borderColor = 'color-mix(in srgb, var(--c-action) 45%, transparent)'

  return (
    <div style={{
      background: 'var(--c-surface)',
      border: `1px solid ${borderColor}`,
      borderRadius: 'var(--radius-md)',
      padding: '12px 14px',
      display: 'flex', flexDirection: 'column', gap: 10,
    }}>
      {/* File line */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <div style={{
          width: 28, height: 28, borderRadius: 'var(--radius-sm)',
          background: isDone ? 'var(--c-success-light, color-mix(in srgb, var(--c-success) 14%, transparent))'
            : isError ? 'color-mix(in srgb, var(--c-danger) 12%, transparent)'
            : isRemoved ? 'var(--c-surface-2)'
            : 'color-mix(in srgb, var(--c-action) 10%, transparent)',
          color: isDone ? 'var(--c-success)'
            : isError ? 'var(--c-danger)'
            : isRemoved ? 'var(--c-text-3)'
            : 'var(--c-action)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          flexShrink: 0,
        }}>
          {isDone ? (
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <polyline points="3 8 6.5 11.5 13 5" />
            </svg>
          ) : isError ? (
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
              <path d="M4 4l8 8M12 4l-8 8" />
            </svg>
          ) : isRemoved ? (
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M3 5h10M6 5V3.5h4V5M5 5l1 9h4l1-9" />
            </svg>
          ) : (
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
              <path d="M9 1H4a1 1 0 0 0-1 1v12a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1V5z" />
              <polyline points="9 1 9 5 13 5" />
            </svg>
          )}
        </div>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{
            fontSize: 13, fontWeight: 700,
            fontFamily: 'var(--font-heading)', color: 'var(--c-text)',
            whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
          }}>
            {entry.title}
          </div>
          <div style={{
            fontSize: 11, color: 'var(--c-text-3)',
            whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
          }}>
            {entry.file.name}
          </div>
        </div>
        {isError && !busy && (
          <button
            type="button"
            onClick={onRetry}
            style={{
              padding: '5px 10px', borderRadius: 'var(--radius-md)',
              background: 'transparent',
              border: '1px solid var(--c-border)',
              color: 'var(--c-text-2)',
              fontSize: 11, fontWeight: 600,
              fontFamily: 'var(--font-heading)',
              cursor: 'pointer',
            }}
          >
            Tentar de novo
          </button>
        )}
      </div>

      {/* Stage list — visible only while flowing OR when settled with detail */}
      {!isQueued && !isRemoved && !isDone && (
        <ul style={{
          listStyle: 'none', padding: 0, margin: 0,
          display: 'flex', flexDirection: 'column', gap: 4,
        }}>
          {STAGE_ORDER.map((s, idx) => {
            const reached = currentIdx > idx || (currentIdx === idx && !isError)
            const isCurrent = currentIdx === idx && !isError
            const isPassed = reached && !isCurrent
            const isFailed = isError && idx === Math.max(0, currentIdx)

            const dotColor = isFailed ? 'var(--c-danger)'
              : isCurrent ? 'var(--c-action)'
              : isPassed ? 'var(--c-success)'
              : 'var(--c-text-3)'
            const textColor = isFailed ? 'var(--c-danger)'
              : isCurrent ? 'var(--c-text)'
              : isPassed ? 'var(--c-text-2)'
              : 'var(--c-text-3)'

            return (
              <li key={s} style={{
                display: 'flex', alignItems: 'center', gap: 9,
                fontSize: 12,
                fontWeight: isCurrent ? 700 : 500,
                color: textColor,
              }}>
                <span style={{
                  width: 8, height: 8, borderRadius: '50%',
                  background: dotColor,
                  flexShrink: 0,
                  animation: isCurrent ? 'wizard-stage-pulse 1.6s ease-out infinite' : undefined,
                }} />
                {STAGE_LABELS[s] ?? s}
              </li>
            )
          })}
        </ul>
      )}

      {/* Settled summary line */}
      {isDone && (
        <div style={{
          fontSize: 12, color: 'var(--c-success)', fontWeight: 600,
          display: 'flex', alignItems: 'center', gap: 6,
        }}>
          Vinculado à campanha
        </div>
      )}
      {isRemoved && (
        <div style={{
          fontSize: 12, color: 'var(--c-text-3)', fontWeight: 500,
          display: 'flex', alignItems: 'center', gap: 6,
        }}>
          Removido — não foi vinculado
        </div>
      )}
      {isError && (
        <div style={{
          fontSize: 12, color: 'var(--c-danger)', fontWeight: 600,
        }}>
          {entry.errorMsg ?? 'Falha durante o processamento'}
        </div>
      )}
      {/* Aviso de material <10s (incident-2026-07-24-pulso-milium): informativo,
          persiste até o fim do fluxo — não bloqueia o vínculo. */}
      {entry.shortWarning && (
        <div role="status" style={{
          marginTop: 4, fontSize: 11.5, lineHeight: 1.5,
          color: 'var(--c-warning)', fontWeight: 500,
        }}>
          ⚠ {entry.shortWarning}
        </div>
      )}
    </div>
  )
}

/* ─────────────────────────────────────────────────────────────────── */

function PrimaryButton({ onClick, disabled, loading, children }) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      style={{
        padding: '9px 18px', borderRadius: 'var(--radius-md)',
        background: disabled ? 'var(--c-surface-2)' : 'var(--c-action)',
        color: disabled ? 'var(--c-text-3)' : '#fff',
        border: 0,
        cursor: disabled ? 'not-allowed' : 'pointer',
        fontSize: 12, fontWeight: 700, fontFamily: 'var(--font-heading)',
        display: 'inline-flex', alignItems: 'center', gap: 8,
        boxShadow: disabled ? 'none' : '0 1px 2px rgba(232,30,117,0.18)',
        transition: 'all 100ms cubic-bezier(0.16,1,0.3,1)',
      }}
    >
      {loading && (
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" style={{
          animation: 'spin 0.8s linear infinite',
        }}>
          <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2.5" strokeOpacity="0.3" />
          <path d="M21 12a9 9 0 0 1-9 9" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
        </svg>
      )}
      {children}
      <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>
    </button>
  )
}
