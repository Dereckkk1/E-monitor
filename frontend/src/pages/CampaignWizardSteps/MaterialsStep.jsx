import { useState } from 'react'
import {
  useCampaignMaterials, useMaterials, useMaterialTypes,
  useLinkCampaignMaterial, useUnlinkCampaignMaterial, useUploadMaterial,
  useUpdateMaterialTypeId,
} from '../../api/hooks'
import TypeIconPill from '../../components/TypeIconPill'
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
  const confirm = useConfirm()
  const [showAdd, setShowAdd] = useState(false)

  const typeById = Object.fromEntries(materialTypes.map(t => [t.id, t]))

  async function handleUnlink(materialId, title) {
    const ok = await confirm(`Desvincular "${title}" desta campanha?`)
    if (!ok) return
    unlink.mutate({ campaignId, materialId })
  }

  return (
    <div style={{ maxWidth: 960, margin: '0 auto' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <h3 style={{ margin: 0, fontSize: 16 }}>Materiais ({cmpMats.length})</h3>
        <button onClick={() => setShowAdd(true)} className="btn btn-primary btn-sm">
          + Adicionar material
        </button>
      </div>

      {cmpMats.length === 0 ? (
        <EmptyState onAdd={() => setShowAdd(true)} />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {cmpMats.map(link => {
            const mat = materialsById[link.material_id]
            if (!mat) return null
            const type = mat.type_id ? typeById[mat.type_id] : null
            return (
              <MaterialCard
                key={link.material_id}
                material={mat}
                link={link}
                type={type}
                allTypes={materialTypes}
                campaignStations={campaignStations}
                onTypeChange={(typeId) => updateType.mutate({ id: mat.id, type_id: typeId })}
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

function EmptyState({ onAdd }) {
  return (
    <div style={{
      padding: 48, textAlign: 'center', background: '#fafbfc',
      border: '1px dashed #e2e8f0', borderRadius: 12,
    }}>
      <div style={{ fontSize: 40, color: '#cbd5e1', marginBottom: 8 }}>🎵</div>
      <h4 style={{ margin: '0 0 6px', color: '#0f172a' }}>Nenhum material ainda</h4>
      <p style={{ margin: '0 0 16px', color: '#64748b', fontSize: 13 }}>
        Vincule materiais existentes da biblioteca do cliente, ou suba arquivos novos.
      </p>
      <button onClick={onAdd} className="btn btn-primary btn-sm">+ Adicionar primeiro material</button>
    </div>
  )
}

function MaterialCard({ material, link, type, allTypes, campaignStations, onTypeChange, onUnlink }) {
  const stationNames = link.target_stations
    .map(id => campaignStations.find(s => s.id === id)?.name)
    .filter(Boolean)

  return (
    <div style={{
      padding: 12, background: '#fff', border: '1px solid #e2e8f0', borderRadius: 8,
      display: 'flex', alignItems: 'center', gap: 12,
    }}>
      <TypeIconPill color={type?.color ?? '#94a3b8'} height={32} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontWeight: 600, color: '#0f172a' }}>{material.title}</div>
        <div style={{ fontSize: 11, color: '#64748b', marginTop: 2 }}>
          {material.duration_seconds ? `${material.duration_seconds.toFixed(1)}s` : '—'}
          {' · '}
          {material.fingerprint_status === 'ready' ? '✓ pronto' :
           material.fingerprint_status === 'generating' ? '⏳ gerando' :
           material.fingerprint_status === 'failed' ? '✗ falhou' :
           'aguardando'}
          {stationNames.length > 0 ? ` · ${stationNames.length} emissora${stationNames.length !== 1 ? 's' : ''}` : ' · sem emissora'}
        </div>
      </div>
      <select
        value={material.type_id ?? ''}
        onChange={e => onTypeChange(e.target.value || null)}
        className="input"
        style={{ width: 160 }}
      >
        <option value="">Sem tipo</option>
        {allTypes.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
      </select>
      <button onClick={onUnlink} className="btn btn-icon btn-danger-ghost btn-sm" title="Desvincular">🗑</button>
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
    <div style={{
      position: 'fixed', inset: 0, background: 'rgba(15,23,42,0.4)', backdropFilter: 'blur(4px)', zIndex: 50,
    }} onClick={onClose}>
      <div onClick={e => e.stopPropagation()} style={{
        position: 'fixed', top: 0, right: 0, bottom: 0, width: 480, background: '#fff',
        boxShadow: '-24px 0 48px -12px rgba(15,23,42,0.25)',
        display: 'flex', flexDirection: 'column',
      }}>
        <div style={{ padding: '18px 22px', borderBottom: '1px solid #e2e8f0', display: 'flex', justifyContent: 'space-between' }}>
          <h3 style={{ margin: 0 }}>Adicionar material</h3>
          <button onClick={onClose} style={{ width: 28, height: 28, border: 0, background: '#f1f5f9', borderRadius: 8, cursor: 'pointer' }}>×</button>
        </div>
        <div style={{ display: 'flex', borderBottom: '1px solid #e2e8f0' }}>
          <button onClick={() => setTab('library')} style={tabStyle(tab === 'library')}>Da biblioteca ({available.length})</button>
          <button onClick={() => setTab('upload')} style={tabStyle(tab === 'upload')}>Subir novo</button>
        </div>
        <div style={{ flex: 1, overflowY: 'auto', padding: 22 }}>
          {tab === 'library' ? (
            available.length === 0 ? (
              <p style={{ color: '#64748b' }}>Nenhum material disponível na biblioteca deste cliente. Use a aba "Subir novo" pra adicionar.</p>
            ) : (
              available.map(m => (
                <label key={m.id} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: 8, borderBottom: '1px solid #f1f5f9' }}>
                  <input type="checkbox" checked={selectedLibIds.has(m.id)}
                    onChange={() => {
                      const next = new Set(selectedLibIds)
                      if (next.has(m.id)) next.delete(m.id); else next.add(m.id)
                      setSelectedLibIds(next)
                    }} />
                  <span style={{ flex: 1 }}>{m.title}</span>
                  <span style={{ fontSize: 11, color: '#64748b' }}>{m.duration_seconds?.toFixed(1)}s</span>
                </label>
              ))
            )
          ) : (
            <div>
              <input
                type="file"
                multiple
                accept=".wav,.mp3,.m4a,.aac,.mpeg"
                onChange={e => { addFiles(e.target.files); e.target.value = '' }}
              />
              <div style={{ marginTop: 12 }}>
                {uploadQueue.map(entry => (
                  <div key={entry.key} style={{
                    padding: 8, background: '#fafbfc', border: '1px solid #e2e8f0', borderRadius: 6, marginBottom: 6,
                  }}>
                    <div style={{ fontSize: 12, marginBottom: 4 }}>📎 {entry.file.name}</div>
                    <input
                      className="input"
                      placeholder="Título"
                      value={entry.title}
                      onChange={e => setUploadQueue(q => q.map(x => x.key === entry.key ? { ...x, title: e.target.value } : x))}
                      disabled={entry.status !== 'pending'}
                    />
                    <select
                      className="input"
                      value={entry.typeId}
                      onChange={e => setUploadQueue(q => q.map(x => x.key === entry.key ? { ...x, typeId: e.target.value } : x))}
                      style={{ marginTop: 4 }}
                      disabled={entry.status !== 'pending'}
                    >
                      <option value="">Selecione o tipo…</option>
                      {materialTypes.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
                    </select>
                    <div style={{ fontSize: 11, marginTop: 4, color: entry.status === 'done' ? '#15803d' : entry.status === 'error' ? '#b91c1c' : '#64748b' }}>
                      {entry.status === 'done' ? '✓ enviado' : entry.status === 'error' ? '✗ erro' : 'pendente'}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
        <div style={{ padding: '14px 22px', borderTop: '1px solid #e2e8f0', display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
          <button onClick={onClose} className="btn btn-secondary btn-sm">Cancelar</button>
          {tab === 'library' ? (
            <button onClick={linkSelected} disabled={selectedLibIds.size === 0} className="btn btn-primary btn-sm">
              Vincular {selectedLibIds.size > 0 ? `(${selectedLibIds.size})` : ''}
            </button>
          ) : (
            <button onClick={submitUploads} disabled={uploadQueue.length === 0 || upload.isPending} className="btn btn-primary btn-sm">
              Subir e vincular
            </button>
          )}
        </div>
      </div>
    </div>
  )
}

const tabStyle = (active) => ({
  flex: 1, padding: '10px 14px', border: 0,
  borderBottom: active ? '2px solid #E81E75' : '2px solid transparent',
  background: '#fff', cursor: 'pointer',
  color: active ? '#E81E75' : '#64748b',
  fontWeight: active ? 600 : 400,
})
