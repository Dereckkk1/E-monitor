import { useState, useMemo } from 'react'
import {
  useCampaignMaterials, useMaterials, useMaterialTypes,
  useLinkCampaignMaterial, useUnlinkCampaignMaterial, useUploadMaterial,
  useUpdateMaterialTypeId, useUpdateCampaignMaterialStations,
} from '../../api/hooks'
import StationAvatar from '../../components/StationAvatar'
import { useConfirm } from '../../components/ConfirmModal'

/**
 * Step 3 of the wizard: link/upload materials to the campaign.
 *
 * Props:
 *  - campaignId: uuid
 *  - clientId: uuid (campaign.client_id)
 *  - materialsById: Record<uuid, Material> (hydrated by parent)
 *  - campaignStations: Array<station> (used as default for new links)
 */
export default function MaterialsStep({ campaignId, clientId, materialsById = {}, campaignStations }) {
  const { data: cmpMats = [] } = useCampaignMaterials(campaignId)
  const { data: materialTypes = [] } = useMaterialTypes()
  const { data: libMats = [] } = useMaterials(clientId)
  const unlink = useUnlinkCampaignMaterial()
  const updateType = useUpdateMaterialTypeId()
  const updateStations = useUpdateCampaignMaterialStations()
  const confirm = useConfirm()
  const [showAdd, setShowAdd] = useState(false)
  // Which material card is currently in stations-editing mode (only one open at a time).
  const [editingStationsFor, setEditingStationsFor] = useState(null)

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
        <EmptyState onAdd={() => setShowAdd(true)} />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          {cmpMats.map(link => {
            const mat = materialsById[link.material_id]
            if (!mat) return null
            const type = mat.type_id ? typeById[mat.type_id] : null
            const isEditing = editingStationsFor === link.material_id
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
  onTypeChange, onSaveStations, onUnlink,
}) {
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

      {/* ── Expandable stations editor ── */}
      {isEditingStations && (
        <StationsInlineEditor
          campaignStations={campaignStations}
          selectedIds={link.target_stations}
          onSave={onSaveStations}
          onClose={onToggleStationsEdit}
        />
      )}
    </div>
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

function EmptyState({ onAdd }) {
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
  const link = useLinkCampaignMaterial()
  const upload = useUploadMaterial()

  const available = libraryMaterials.filter(m => !alreadyLinkedIds.has(m.id))
  const defaultStationIds = campaignStations.map(s => s.id)

  async function linkSelected() {
    for (const id of selectedLibIds) {
      await link.mutateAsync({ campaignId, material_id: id, target_stations: defaultStationIds })
    }
    onClose()
  }

  function addFiles(files) {
    const entries = Array.from(files)
      .filter(f => /\.(wav|mp3|m4a|aac|mpeg)$/i.test(f.name))
      .map(file => ({
        key: `${file.name}-${Date.now()}-${Math.random()}`,
        file, title: file.name.replace(/\.[^.]+$/, ''), typeId: '', status: 'pending',
      }))
    setUploadQueue(q => [...q, ...entries])
  }

  async function submitUploads() {
    for (const entry of uploadQueue) {
      if (entry.status !== 'pending') continue
      const fd = new FormData()
      fd.append('client_id', clientId)
      fd.append('title', entry.title)
      if (entry.typeId) fd.append('type_id', entry.typeId)
      fd.append('audio', entry.file)
      try {
        const mat = await upload.mutateAsync(fd)
        await link.mutateAsync({ campaignId, material_id: mat.id, target_stations: defaultStationIds })
        setUploadQueue(q => q.map(e => e.key === entry.key ? { ...e, status: 'done' } : e))
      } catch {
        setUploadQueue(q => q.map(e => e.key === entry.key ? { ...e, status: 'error' } : e))
      }
    }
    setTimeout(onClose, 500)
  }

  return (
    <div
      style={{
        position: 'fixed', inset: 0,
        background: 'rgba(15,23,42,0.45)', backdropFilter: 'blur(6px)',
        zIndex: 50,
        animation: 'wizard-fade-in 200ms cubic-bezier(0.16,1,0.3,1)',
      }}
      onClick={onClose}
    >
      <style>{`
        @keyframes wizard-fade-in { from { opacity: 0; } to { opacity: 1; } }
        @keyframes wizard-slide-in { from { transform: translateX(20px); opacity: 0; } to { transform: translateX(0); opacity: 1; } }
      `}</style>
      <aside
        onClick={e => e.stopPropagation()}
        style={{
          position: 'fixed', top: 0, right: 0, bottom: 0, width: 540,
          background: 'var(--c-surface)',
          boxShadow: '-24px 0 48px -12px rgba(15,23,42,0.25)',
          display: 'flex', flexDirection: 'column',
          animation: 'wizard-slide-in 300ms cubic-bezier(0.16,1,0.3,1)',
        }}
      >
        {/* Header */}
        <div style={{
          padding: '20px 24px', borderBottom: '1px solid var(--c-border)',
          display: 'flex', justifyContent: 'space-between', alignItems: 'center',
        }}>
          <div>
            <span style={{
              fontSize: 10, fontWeight: 700, letterSpacing: '0.12em',
              color: 'var(--c-action)', textTransform: 'uppercase',
            }}>
              Adicionar à campanha
            </span>
            <h3 style={{
              margin: 0, fontFamily: 'var(--font-heading)', fontWeight: 700,
              fontSize: 18, color: 'var(--c-text)', marginTop: 4,
            }}>
              {tab === 'library' ? 'Vincular da biblioteca' : 'Upload de novos áudios'}
            </h3>
          </div>
          <button
            onClick={onClose}
            aria-label="Fechar"
            style={{
              width: 30, height: 30, border: 0,
              background: 'var(--c-surface-2)', color: 'var(--c-text-2)',
              borderRadius: 'var(--radius-md)', cursor: 'pointer',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
            }}
          >
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round">
              <path d="M4 4l8 8M12 4l-8 8" />
            </svg>
          </button>
        </div>

        {/* Tabs */}
        <div style={{ display: 'flex', borderBottom: '1px solid var(--c-border)', background: 'var(--c-bg)' }}>
          <TabBtn active={tab === 'library'} onClick={() => setTab('library')}>
            Biblioteca <Counter>{available.length}</Counter>
          </TabBtn>
          <TabBtn active={tab === 'upload'} onClick={() => setTab('upload')}>
            Upload {uploadQueue.length > 0 && <Counter>{uploadQueue.length}</Counter>}
          </TabBtn>
        </div>

        {/* Body */}
        <div style={{ flex: 1, overflowY: 'auto', padding: 22 }}>
          {tab === 'library' ? (
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
                        cursor: 'pointer',
                        transition: 'all 100ms',
                      }}
                    >
                      <input
                        type="checkbox"
                        checked={checked}
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
            />
          )}
        </div>

        {/* Footer */}
        <div style={{
          padding: '14px 22px', borderTop: '1px solid var(--c-border)',
          display: 'flex', justifyContent: 'flex-end', gap: 10,
          background: 'var(--c-surface)',
        }}>
          <button
            onClick={onClose}
            style={{
              padding: '9px 16px', borderRadius: 'var(--radius-md)',
              background: 'transparent', color: 'var(--c-text-2)',
              border: '1px solid var(--c-border)', cursor: 'pointer',
              fontSize: 12, fontWeight: 600, fontFamily: 'var(--font-heading)',
            }}
          >
            Cancelar
          </button>
          {tab === 'library' ? (
            <button
              onClick={linkSelected}
              disabled={selectedLibIds.size === 0}
              style={{
                padding: '9px 18px', borderRadius: 'var(--radius-md)',
                background: selectedLibIds.size === 0 ? 'var(--c-surface-2)' : 'var(--c-action)',
                color: selectedLibIds.size === 0 ? 'var(--c-text-3)' : '#fff',
                border: 0, cursor: selectedLibIds.size === 0 ? 'not-allowed' : 'pointer',
                fontSize: 12, fontWeight: 700, fontFamily: 'var(--font-heading)',
              }}
            >
              Vincular {selectedLibIds.size > 0 ? `(${selectedLibIds.size})` : ''}
            </button>
          ) : (
            <button
              onClick={submitUploads}
              disabled={uploadQueue.length === 0 || upload.isPending}
              style={{
                padding: '9px 18px', borderRadius: 'var(--radius-md)',
                background: uploadQueue.length === 0 ? 'var(--c-surface-2)' : 'var(--c-action)',
                color: uploadQueue.length === 0 ? 'var(--c-text-3)' : '#fff',
                border: 0, cursor: uploadQueue.length === 0 ? 'not-allowed' : 'pointer',
                fontSize: 12, fontWeight: 700, fontFamily: 'var(--font-heading)',
              }}
            >
              Subir e vincular
            </button>
          )}
        </div>
      </aside>
    </div>
  )
}

function TabBtn({ active, onClick, children }) {
  return (
    <button
      onClick={onClick}
      style={{
        flex: 1, padding: '12px 14px', border: 0,
        borderBottom: active ? '2px solid var(--c-action)' : '2px solid transparent',
        background: 'transparent', cursor: 'pointer',
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

function UploadTab({ uploadQueue, setUploadQueue, addFiles, materialTypes }) {
  const [dragOver, setDragOver] = useState(false)

  function onDrop(e) {
    e.preventDefault()
    setDragOver(false)
    if (e.dataTransfer.files?.length) addFiles(e.dataTransfer.files)
  }

  return (
    <div>
      <label
        onDragOver={e => { e.preventDefault(); setDragOver(true) }}
        onDragLeave={() => setDragOver(false)}
        onDrop={onDrop}
        style={{
          display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
          gap: 6, padding: '28px 20px',
          background: dragOver ? 'var(--c-action-light, rgba(232,30,117,0.08))' : 'var(--c-bg)',
          border: `2px dashed ${dragOver ? 'var(--c-action)' : 'var(--c-border)'}`,
          borderRadius: 'var(--radius-lg)',
          cursor: 'pointer',
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
          onChange={e => { addFiles(e.target.files); e.target.value = '' }}
          style={{ display: 'none' }}
        />
      </label>

      {uploadQueue.length > 0 && (
        <div style={{ marginTop: 16, display: 'flex', flexDirection: 'column', gap: 8 }}>
          {uploadQueue.map(entry => {
            const statusColor = entry.status === 'done' ? 'var(--c-success)'
              : entry.status === 'error' ? 'var(--c-danger)'
              : 'var(--c-text-2)'
            return (
              <div key={entry.key} style={{
                padding: 12, background: 'var(--c-bg)',
                border: '1px solid var(--c-border)', borderRadius: 'var(--radius-md)',
                display: 'flex', flexDirection: 'column', gap: 8,
              }}>
                <div style={{
                  fontSize: 11, color: 'var(--c-text-3)',
                  whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
                }}>
                  {entry.file.name}
                </div>
                <input
                  className="input"
                  placeholder="Título do material"
                  value={entry.title}
                  onChange={e => setUploadQueue(q => q.map(x => x.key === entry.key ? { ...x, title: e.target.value } : x))}
                  disabled={entry.status !== 'pending'}
                />
                <select
                  className="input"
                  value={entry.typeId}
                  onChange={e => setUploadQueue(q => q.map(x => x.key === entry.key ? { ...x, typeId: e.target.value } : x))}
                  disabled={entry.status !== 'pending'}
                  style={{ fontSize: 12 }}
                >
                  <option value="">Sem tipo (opcional)</option>
                  {materialTypes.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
                </select>
                <span style={{ fontSize: 11, fontWeight: 600, color: statusColor }}>
                  {entry.status === 'done' ? '✓ Enviado'
                    : entry.status === 'error' ? '✕ Erro no envio'
                    : '○ Aguardando upload'}
                </span>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
