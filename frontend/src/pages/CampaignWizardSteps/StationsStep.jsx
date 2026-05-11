import { useState, useEffect, useMemo } from 'react'
import { useStations, useUpdateCampaignStations } from '../../api/hooks'
import RSelect from '../../components/RSelect'

/**
 * Step 2 of the wizard: pick stations via RSelect multi-select with server-side search.
 *
 * Props:
 *  - campaignId: uuid (set after step 1 completes)
 *  - allStations: Array<station> (pre-fetched in parent — used to render labels for currently-selected IDs that may not be in latest search results)
 *  - currentSelection: Array<uuid> (campaign.target_stations)
 */
export default function StationsStep({ campaignId, allStations, currentSelection }) {
  const [stationInput, setStationInput] = useState('')
  const [debouncedInput, setDebouncedInput] = useState('')

  // The selected options carry their own label (so they stay visible even after server search filters them out)
  const [selectedOpts, setSelectedOpts] = useState(() =>
    (currentSelection ?? [])
      .map(id => allStations.find(s => s.id === id))
      .filter(Boolean)
      .map(s => toOption(s))
  )

  // Re-sync if currentSelection changes externally (e.g. when entering edit mode)
  useEffect(() => {
    setSelectedOpts(
      (currentSelection ?? [])
        .map(id => allStations.find(s => s.id === id))
        .filter(Boolean)
        .map(s => toOption(s))
    )
  }, [currentSelection, allStations])

  // Debounce search input
  useEffect(() => {
    const t = setTimeout(() => setDebouncedInput(stationInput), 400)
    return () => clearTimeout(t)
  }, [stationInput])

  const { data: searchData, isFetching } = useStations({
    q: debouncedInput || undefined,
    limit: 25,
  })
  const searchResults = searchData?.data ?? searchData ?? []

  // Build options: server results + any currently-selected that aren't in the server results
  // (so they don't disappear from the chips when the user types something unrelated)
  const stationOptions = useMemo(() => {
    const results = searchResults.map(toOption)
    const resultIds = new Set(results.map(o => o.value))
    const extra = selectedOpts.filter(o => !resultIds.has(o.value))
    return [...results, ...extra]
  }, [searchResults, selectedOpts])

  const updateCampaign = useUpdateCampaignStations()

  // Save with debounce when selection changes (not on every keystroke)
  useEffect(() => {
    if (!campaignId) return
    const ids = selectedOpts.map(o => o.value)
    const sameAsCurrent = ids.length === currentSelection.length &&
      ids.every(id => currentSelection.includes(id))
    if (sameAsCurrent) return
    const t = setTimeout(() => {
      updateCampaign.mutate({ id: campaignId, targetStations: ids })
    }, 500)
    return () => clearTimeout(t)
  }, [selectedOpts, campaignId])

  return (
    <div style={{ maxWidth: 720, margin: '0 auto' }}>
      <h3 style={{ marginTop: 0, fontSize: 16 }}>Selecione as emissoras</h3>
      <p style={{ color: '#64748b', fontSize: 13, marginTop: 0, marginBottom: 20 }}>
        Pesquise por nome, cidade, dial ou banda. As emissoras selecionadas vão monitorar todos os materiais
        desta campanha por padrão (pode refinar por material na etapa 4).
      </p>

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
        noOptionsMessage={() => debouncedInput ? 'Nenhuma emissora encontrada' : 'Comece a digitar pra buscar'}
        loadingMessage={() => 'Buscando…'}
      />

      <p style={{ marginTop: 12, fontSize: 12, color: '#64748b' }}>
        {selectedOpts.length === 0
          ? 'Nenhuma emissora selecionada ainda.'
          : `${selectedOpts.length} emissora${selectedOpts.length !== 1 ? 's' : ''} selecionada${selectedOpts.length !== 1 ? 's' : ''}.`}
      </p>
    </div>
  )
}

// toOption converts a station object to RSelect's {value, label} shape.
function toOption(s) {
  const freq = s.frequency_mhz != null ? ` ${s.frequency_mhz}` : ''
  const city = s.city ? ` · ${s.city}` : ''
  return { value: s.id, label: `${s.name} (${s.band}${freq})${city}` }
}
