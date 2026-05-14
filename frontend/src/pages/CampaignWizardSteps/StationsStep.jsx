import { useState, useEffect, useMemo } from 'react'
import { useStations, useUpdateCampaignStations } from '../../api/hooks'
import RSelect from '../../components/RSelect'
import StationAvatar from '../../components/StationAvatar'

/**
 * Step 2 of the wizard: pick stations via RSelect multi-select with server-side
 * search, then surface the current selection as a visual grid of station cards
 * so the user can review and remove without re-opening the dropdown.
 *
 * Props:
 *  - campaignId: uuid (set after step 1 completes)
 *  - allStations: Array<station> (pre-fetched, so we can render labels for IDs
 *    that aren't in the latest search response)
 *  - currentSelection: Array<uuid> (campaign.target_stations)
 */
export default function StationsStep({ campaignId, allStations, currentSelection }) {
  const [stationInput, setStationInput] = useState('')
  const [debouncedInput, setDebouncedInput] = useState('')

  const [selectedOpts, setSelectedOpts] = useState(() =>
    (currentSelection ?? [])
      .map(id => allStations.find(s => s.id === id))
      .filter(Boolean)
      .map(toOption)
  )

  // Re-sync if currentSelection changes externally (edit mode hydration)
  useEffect(() => {
    setSelectedOpts(
      (currentSelection ?? [])
        .map(id => allStations.find(s => s.id === id))
        .filter(Boolean)
        .map(toOption)
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

  const stationOptions = useMemo(() => {
    const results = searchResults.map(toOption)
    const resultIds = new Set(results.map(o => o.value))
    const extra = selectedOpts.filter(o => !resultIds.has(o.value))
    return [...results, ...extra]
  }, [searchResults, selectedOpts])

  const updateCampaign = useUpdateCampaignStations()

  // Save with debounce (not on every keystroke).
  //
  // Two hydration guards prevent data loss when `allStations` arrives later
  // than the auto-save's 500ms window — without these the selectedOpts
  // initializer (which uses `allStations.find`) starts empty and the effect
  // would auto-save target_stations=[] over a populated list:
  //
  //   1. Skip when `allStations` hasn't loaded yet.
  //   2. Skip when one or more ids in `currentSelection` aren't in
  //      `allStations` — that's a hydration race, not a user-initiated
  //      removal. The save will fire on the next render once allStations
  //      is complete and selectedOpts re-syncs.
  useEffect(() => {
    if (!campaignId) return
    if (!allStations || allStations.length === 0) return
    const ids = selectedOpts.map(o => o.value)
    const sameAsCurrent = ids.length === currentSelection.length &&
      ids.every(id => currentSelection.includes(id))
    if (sameAsCurrent) return
    const allCurrentResolved = currentSelection.every(
      id => allStations.some(s => s.id === id)
    )
    if (!allCurrentResolved) return
    const t = setTimeout(() => {
      updateCampaign.mutate({ id: campaignId, targetStations: ids })
    }, 500)
    return () => clearTimeout(t)
  }, [selectedOpts, campaignId, allStations, currentSelection])

  function removeOne(id) {
    setSelectedOpts(opts => opts.filter(o => o.value !== id))
  }

  function clearAll() {
    setSelectedOpts([])
  }

  const stationsById = useMemo(
    () => Object.fromEntries(allStations.map(s => [s.id, s])),
    [allStations]
  )

  // Group selected stations by state for a more useful overview
  const grouped = useMemo(() => {
    const m = new Map()
    for (const opt of selectedOpts) {
      const st = stationsById[opt.value]
      const key = st?.state || '—'
      if (!m.has(key)) m.set(key, [])
      m.get(key).push(st ?? { id: opt.value, name: opt.label })
    }
    return [...m.entries()].sort((a, b) => a[0].localeCompare(b[0]))
  }, [selectedOpts, stationsById])

  const count = selectedOpts.length

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 28 }}>

      {/* Section title */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6, maxWidth: 720 }}>
        <h2 style={{
          margin: 0, fontFamily: 'var(--font-heading)', fontWeight: 700,
          fontSize: 24, color: 'var(--c-text)', letterSpacing: '-0.01em',
        }}>
          Onde a campanha vai tocar?
        </h2>
        <p style={{ margin: 0, color: 'var(--c-text-2)', fontSize: 13, lineHeight: 1.55 }}>
          Busque por nome, cidade, dial ou banda. As emissoras escolhidas aqui
          monitoram <strong style={{ color: 'var(--c-text)' }}>todos</strong> os
          materiais por padrão — você refina por material no passo 4.
        </p>
      </div>

      {/* Hero counter + search */}
      <div style={{
        display: 'grid',
        gridTemplateColumns: '180px minmax(0, 1fr)',
        gap: 20,
        alignItems: 'stretch',
      }} className="stations-hero">
        <div style={{
          background: count > 0 ? 'var(--c-action-light, rgba(232,30,117,0.08))' : 'var(--c-bg)',
          border: `1px solid ${count > 0 ? 'transparent' : 'var(--c-border)'}`,
          borderRadius: 'var(--radius-xl)',
          padding: '18px 20px',
          display: 'flex', flexDirection: 'column', justifyContent: 'center',
          transition: 'all 250ms cubic-bezier(0.16,1,0.3,1)',
        }}>
          <span style={{
            fontSize: 10, fontWeight: 700, letterSpacing: '0.12em',
            color: count > 0 ? 'var(--c-action)' : 'var(--c-text-3)',
            textTransform: 'uppercase',
          }}>
            Selecionadas
          </span>
          <span style={{
            fontSize: 42, fontWeight: 700, lineHeight: 1,
            fontFamily: 'var(--font-heading)',
            color: count > 0 ? 'var(--c-action)' : 'var(--c-text-3)',
            letterSpacing: '-0.02em',
            marginTop: 4,
          }}>
            {count}
          </span>
          <span style={{ fontSize: 11, color: 'var(--c-text-3)', marginTop: 4 }}>
            emissora{count !== 1 ? 's' : ''}{grouped.length > 0 && ` em ${grouped.length} UF${grouped.length !== 1 ? 's' : ''}`}
          </span>
        </div>

        <div style={{
          background: 'var(--c-surface)',
          border: '1px solid var(--c-border)',
          borderRadius: 'var(--radius-xl)',
          padding: 16,
          display: 'flex', flexDirection: 'column', gap: 10,
        }}>
          <label style={{
            fontSize: 12, fontWeight: 600, color: 'var(--c-text)',
            fontFamily: 'var(--font-heading)',
            display: 'flex', alignItems: 'center', gap: 6,
          }}>
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round">
              <circle cx="7" cy="7" r="5" /><path d="M11 11l3 3" />
            </svg>
            Adicionar emissoras
          </label>
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
            placeholder="Digite o nome, cidade, dial…"
            closeMenuOnSelect={false}
            controlShouldRenderValue={false}
            noOptionsMessage={() => debouncedInput ? 'Nenhuma emissora encontrada' : 'Comece a digitar pra buscar'}
            loadingMessage={() => 'Buscando…'}
          />
          <span style={{ fontSize: 11, color: 'var(--c-text-3)' }}>
            Múltiplas seleções permitidas — clique nas emissoras abaixo para remover.
          </span>
        </div>
      </div>

      {/* Selected stations preview */}
      {count === 0 ? (
        <EmptyPreview />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <span style={{
              fontSize: 11, fontWeight: 700, letterSpacing: '0.10em',
              color: 'var(--c-text-3)', textTransform: 'uppercase',
            }}>
              Selecionadas · agrupadas por UF
            </span>
            <button
              onClick={clearAll}
              style={{
                background: 'transparent', border: 0, cursor: 'pointer',
                fontSize: 11, color: 'var(--c-danger)', fontWeight: 600,
                padding: '4px 8px', borderRadius: 'var(--radius-sm)',
                fontFamily: 'var(--font-heading)',
              }}
            >
              Limpar tudo
            </button>
          </div>

          {grouped.map(([state, stations]) => (
            <div key={state} style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              <div style={{
                display: 'flex', alignItems: 'center', gap: 8,
                fontSize: 11, color: 'var(--c-text-2)', fontWeight: 600,
                fontFamily: 'var(--font-heading)',
              }}>
                <span style={{
                  padding: '2px 8px', background: 'var(--c-surface-2)',
                  borderRadius: 'var(--radius-full)',
                  fontSize: 10, fontWeight: 700, letterSpacing: '0.06em',
                  color: 'var(--c-text)',
                }}>
                  {state}
                </span>
                <span style={{ color: 'var(--c-text-3)' }}>
                  {stations.length} emissora{stations.length !== 1 ? 's' : ''}
                </span>
              </div>
              <div style={{
                display: 'grid',
                gridTemplateColumns: 'repeat(auto-fill, minmax(240px, 1fr))',
                gap: 8,
              }}>
                {stations.map(s => (
                  <StationChip key={s.id} station={s} onRemove={() => removeOne(s.id)} />
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function StationChip({ station, onRemove }) {
  const freq = station.frequency_mhz != null ? ` ${station.frequency_mhz}` : ''
  const place = [station.city, station.state].filter(Boolean).join(' / ')
  return (
    <div
      style={{
        background: 'var(--c-surface)',
        border: '1px solid var(--c-border)',
        borderRadius: 'var(--radius-md)',
        padding: '8px 10px',
        display: 'flex', alignItems: 'center', gap: 10,
        transition: 'all 150ms cubic-bezier(0.16,1,0.3,1)',
        minWidth: 0,
      }}
      onMouseEnter={e => {
        e.currentTarget.style.borderColor = 'var(--c-action-300, #F472B6)'
        e.currentTarget.style.transform = 'translateY(-1px)'
        e.currentTarget.style.boxShadow = 'var(--shadow-sm)'
      }}
      onMouseLeave={e => {
        e.currentTarget.style.borderColor = 'var(--c-border)'
        e.currentTarget.style.transform = 'translateY(0)'
        e.currentTarget.style.boxShadow = 'none'
      }}
    >
      <StationAvatar station={station} size={28} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{
          fontSize: 12, fontWeight: 600, color: 'var(--c-text)',
          whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
          fontFamily: 'var(--font-heading)',
        }}>
          {station.name}
        </div>
        <div style={{
          fontSize: 10, color: 'var(--c-text-3)',
          whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
        }}>
          {station.band || ''}{freq}{place ? ` · ${place}` : ''}
        </div>
      </div>
      <button
        onClick={onRemove}
        title="Remover"
        aria-label="Remover emissora"
        style={{
          width: 22, height: 22, borderRadius: 'var(--radius-sm)',
          background: 'transparent', border: 0,
          color: 'var(--c-text-3)', cursor: 'pointer',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          flexShrink: 0,
          transition: 'all 100ms',
        }}
        onMouseEnter={e => {
          e.currentTarget.style.background = '#fee2e2'
          e.currentTarget.style.color = 'var(--c-danger)'
        }}
        onMouseLeave={e => {
          e.currentTarget.style.background = 'transparent'
          e.currentTarget.style.color = 'var(--c-text-3)'
        }}
      >
        <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round">
          <path d="M4 4l8 8M12 4l-8 8" />
        </svg>
      </button>
    </div>
  )
}

function EmptyPreview() {
  // Ghost preview: shows what the selected list will look like.
  return (
    <div style={{
      position: 'relative',
      padding: '36px 24px',
      background: 'var(--c-bg)',
      border: '1px dashed var(--c-border)',
      borderRadius: 'var(--radius-xl)',
      overflow: 'hidden',
    }}>
      {/* Ghost cards */}
      <div style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fill, minmax(240px, 1fr))',
        gap: 8, opacity: 0.35,
        pointerEvents: 'none',
      }}>
        {Array.from({ length: 6 }).map((_, i) => (
          <div key={i} style={{
            background: 'var(--c-surface)',
            border: '1px solid var(--c-border)',
            borderRadius: 'var(--radius-md)',
            padding: '8px 10px',
            display: 'flex', alignItems: 'center', gap: 10,
          }}>
            <div style={{ width: 28, height: 28, borderRadius: '50%', background: 'var(--c-surface-2)' }} />
            <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 4 }}>
              <div style={{ height: 8, background: 'var(--c-surface-2)', borderRadius: 4, width: '70%' }} />
              <div style={{ height: 6, background: 'var(--c-surface-2)', borderRadius: 3, width: '45%' }} />
            </div>
          </div>
        ))}
      </div>

      {/* Centered prompt */}
      <div style={{
        position: 'absolute', inset: 0,
        display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
        gap: 6,
        background: 'linear-gradient(to bottom, rgba(248,250,252,0.4), rgba(248,250,252,0.95))',
      }}>
        <div style={{
          width: 44, height: 44, borderRadius: '50%',
          background: 'var(--c-surface)', boxShadow: 'var(--shadow-sm)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          color: 'var(--c-action)', marginBottom: 4,
        }}>
          <svg width="20" height="20" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
            <rect x="2" y="6" width="12" height="7" rx="1.5" />
            <path d="M5 4.5l5-2.5" />
            <circle cx="11" cy="9.5" r="1.2" />
          </svg>
        </div>
        <span style={{
          fontFamily: 'var(--font-heading)', fontWeight: 700,
          fontSize: 14, color: 'var(--c-text)',
        }}>
          Nenhuma emissora selecionada ainda
        </span>
        <span style={{ fontSize: 12, color: 'var(--c-text-2)' }}>
          Use o campo acima para buscar e adicionar.
        </span>
      </div>
    </div>
  )
}

function toOption(s) {
  const freq = s.frequency_mhz != null ? ` ${s.frequency_mhz}` : ''
  const city = s.city ? ` · ${s.city}` : ''
  return { value: s.id, label: `${s.name} (${s.band}${freq})${city}` }
}
