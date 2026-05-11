import { useState, useEffect, useMemo } from 'react'
import { useStations, useUpdateCampaignStations } from '../../api/hooks'
import StationAvatar from '../../components/StationAvatar'

/**
 * Step 2 of the wizard: pick stations for the campaign.
 *
 * Props:
 *  - campaignId: uuid (set after step 1 completes)
 *  - allStations: Array<station> (pre-fetched in parent)
 *  - currentSelection: Array<uuid> (campaign.target_stations)
 */
export default function StationsStep({ campaignId, allStations, currentSelection }) {
  const [searchQ, setSearchQ] = useState('')
  const [debouncedQ, setDebouncedQ] = useState('')
  const [bandFilter, setBandFilter] = useState('')
  const [selectedIds, setSelectedIds] = useState(new Set(currentSelection))

  useEffect(() => { setSelectedIds(new Set(currentSelection)) }, [currentSelection])
  useEffect(() => {
    const t = setTimeout(() => setDebouncedQ(searchQ), 300)
    return () => clearTimeout(t)
  }, [searchQ])

  const { data: searchData } = useStations({ q: debouncedQ, band: bandFilter || undefined, limit: 100 })
  const searchResults = searchData?.data ?? searchData ?? []

  const updateCampaign = useUpdateCampaignStations()

  // Save on every change (debounced)
  useEffect(() => {
    if (!campaignId) return
    const arr = [...selectedIds]
    const sameAsCurrent = arr.length === currentSelection.length &&
      arr.every(id => currentSelection.includes(id))
    if (sameAsCurrent) return
    const t = setTimeout(() => {
      updateCampaign.mutate({ id: campaignId, targetStations: arr })
    }, 600)
    return () => clearTimeout(t)
  }, [selectedIds, campaignId])

  function toggle(id) {
    setSelectedIds(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id); else next.add(id)
      return next
    })
  }

  const selectedStations = useMemo(
    () => [...selectedIds].map(id => allStations.find(s => s.id === id)).filter(Boolean),
    [selectedIds, allStations]
  )

  return (
    <div style={{ maxWidth: 960, margin: '0 auto' }}>
      <h3 style={{ marginTop: 0, fontSize: 16 }}>Selecione as emissoras</h3>

      <div style={{ display: 'flex', gap: 8, marginBottom: 16, flexWrap: 'wrap' }}>
        <input
          className="input"
          placeholder="Buscar por nome, cidade, dial…"
          value={searchQ}
          onChange={e => setSearchQ(e.target.value)}
          style={{ flex: 1, minWidth: 220 }}
        />
        <select className="input" value={bandFilter} onChange={e => setBandFilter(e.target.value)} style={{ width: 120 }}>
          <option value="">Todas bandas</option>
          <option value="AM">AM</option>
          <option value="FM">FM</option>
        </select>
      </div>

      {selectedStations.length > 0 && (
        <div style={{
          marginBottom: 16, padding: 12, background: '#fdf2f8',
          border: '1px solid #f9a8d4', borderRadius: 8,
        }}>
          <div style={{ fontSize: 11, color: '#be185d', fontWeight: 600, marginBottom: 6, textTransform: 'uppercase' }}>
            {selectedStations.length} emissora{selectedStations.length !== 1 ? 's' : ''} selecionada{selectedStations.length !== 1 ? 's' : ''}
          </div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
            {selectedStations.map(s => (
              <span key={s.id} style={{
                padding: '4px 10px', background: '#fff', border: '1px solid #f9a8d4',
                borderRadius: 999, fontSize: 11, fontWeight: 600, color: '#be185d',
                display: 'inline-flex', alignItems: 'center', gap: 6,
              }}>
                {s.name}
                <button onClick={() => toggle(s.id)} style={{
                  border: 0, background: 'transparent', color: '#be185d', cursor: 'pointer', padding: 0,
                }}>×</button>
              </span>
            ))}
          </div>
        </div>
      )}

      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        {searchResults.map(s => {
          const checked = selectedIds.has(s.id)
          return (
            <label key={s.id} style={{
              display: 'flex', alignItems: 'center', gap: 12, padding: 10,
              border: '1px solid #e2e8f0', borderRadius: 8, cursor: 'pointer',
              background: checked ? '#fdf2f8' : '#fff',
            }}>
              <input type="checkbox" checked={checked} onChange={() => toggle(s.id)} />
              <StationAvatar station={s} size={28} />
              <div style={{ flex: 1 }}>
                <div style={{ fontWeight: 600 }}>{s.name}</div>
                <div style={{ fontSize: 11, color: '#64748b' }}>
                  {s.band} {s.frequency_mhz ?? ''} · {s.city ?? ''} · {s.monitoring_status ?? ''}
                </div>
              </div>
            </label>
          )
        })}
        {searchResults.length === 0 && (
          <div style={{ padding: 24, textAlign: 'center', color: '#64748b' }}>
            Nenhuma emissora encontrada.
          </div>
        )}
      </div>
    </div>
  )
}
