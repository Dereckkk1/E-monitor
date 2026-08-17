import { forwardRef, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react'
import {
  useCampaignPricing, useUpsertStationPricing, useDeleteStationPricing,
  useMaterialTypes, useUpdateCampaignFixedCPM,
} from '../../api/hooks'
import StationAvatar from '../../components/StationAvatar'

// ── Helpers ─────────────────────────────────────────────────────────────────

const MODE_CONSOLIDATED = 'consolidated'
const MODE_PER_INSERTION = 'per_insertion'

const BRL = new Intl.NumberFormat('pt-BR', {
  style: 'currency', currency: 'BRL',
  minimumFractionDigits: 2, maximumFractionDigits: 2,
})

function fmtBRL(n) {
  if (n == null || Number.isNaN(n)) return '—'
  return BRL.format(n)
}

// Converte string digitada (com vírgula/ponto) em number. Retorna null
// quando vazio. Mantém comportamento previsível pra usuário brasileiro
// (1.500,75 vira 1500.75; 1500.75 também aceito).
function parseCurrency(str) {
  if (str == null || String(str).trim() === '') return null
  const normalized = String(str)
    .replace(/\s/g, '')
    .replace(/\./g, '')   // remove milhares
    .replace(',', '.')    // vírgula vira ponto decimal
  const n = Number(normalized)
  return Number.isFinite(n) ? n : null
}

// Validação retornada por cada card → bubbles up pro parent decidir se pode
// avançar. Inclui um erro humano e um boolean "completo".
function validateStation(draft, typesInScopeForStation) {
  if (!draft) return { ok: false, reason: 'sem cadastro' }
  if (draft.mode === MODE_CONSOLIDATED) {
    if (draft.consolidated == null || draft.consolidated < 0) {
      return { ok: false, reason: 'valor consolidado obrigatório' }
    }
    return { ok: true, reason: '' }
  }
  if (draft.mode === MODE_PER_INSERTION) {
    const needed = typesInScopeForStation
    if (needed.length === 0) {
      // Não há tipos pra cobrar → o cara escolheu per_insertion mas a estação
      // não tem material linkado. Forçamos a trocar pra consolidado.
      return { ok: false, reason: 'sem tipos pra precificar nessa emissora' }
    }
    for (const t of needed) {
      const v = draft.perType?.[t.id]
      if (v == null || v < 0) {
        return { ok: false, reason: `${t.name} sem valor` }
      }
    }
    return { ok: true, reason: '' }
  }
  return { ok: false, reason: 'modo inválido' }
}

// ── Component ───────────────────────────────────────────────────────────────

/**
 * Step 5 — Valores por emissora.
 *
 * Props:
 *  - campaignId
 *  - campaignStations: Array<station>  (do target_stations da campanha)
 *  - campaignMaterials: Array<{material_id, target_stations}>
 *  - materialsById: Record<uuid, Material>
 *
 * Ref API:
 *  - saveAll() → Promise<boolean>   (chamado pelo wizard antes de finalizar)
 *  - isValid() → boolean
 */
const PricingStep = forwardRef(function PricingStep({
  campaignId, campaignStations, campaignMaterials, materialsById = {},
  distributionRules = [], initialFixedCPM = null,
}, ref) {
  const { data: pricingList = [], isLoading } = useCampaignPricing(campaignId)
  const { data: materialTypes = [] } = useMaterialTypes()
  const upsert = useUpsertStationPricing()
  const del = useDeleteStationPricing()
  const updateFixedCPM = useUpdateCampaignFixedCPM()

  // CPM fixo opcional da campanha. null = não setado → /campaigns, /insights e
  // dashboard usam o cálculo dinâmico (executado / impactos × 1000). Quando
  // preenchido aqui, vira a fonte da verdade nas telas de exibição.
  const initialFixedCPMNum = useMemo(() => {
    const n = Number(initialFixedCPM)
    return Number.isFinite(n) ? n : null
  }, [initialFixedCPM])
  const [fixedCPM, setFixedCPM] = useState(initialFixedCPMNum)
  // Snapshot do valor original pra detectar mudança no saveAll. Atualiza só
  // depois que conseguimos persistir, evitando reenviar PATCH idêntico.
  const savedFixedCPMRef = useRef(initialFixedCPMNum)
  useEffect(() => {
    setFixedCPM(initialFixedCPMNum)
    savedFixedCPMRef.current = initialFixedCPMNum
  }, [initialFixedCPMNum])

  // typesInScope[stationId] = Array<MaterialType> presentes na estação dentro
  // da campanha. União de tipos vindos de materiais linkados E tipos cobertos
  // por regras de distribuição (spec 2026-05-25 §4.6). Permite cadastrar
  // unit_value pra um tipo antes mesmo do áudio chegar.
  const typesInScope = useMemo(() => {
    const m = new Map()
    const addType = (sid, type) => {
      if (!m.has(sid)) m.set(sid, new Map())
      m.get(sid).set(type.id, type)
    }
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      if (!mat?.type_id) continue
      const type = materialTypes.find(t => t.id === mat.type_id)
      if (!type) continue
      for (const sid of cm.target_stations ?? []) addType(sid, type)
    }
    for (const r of distributionRules) {
      const type = materialTypes.find(t => t.id === r.type_id)
      if (!type) continue
      for (const sid of r.station_ids) addType(sid, type)
    }
    const out = {}
    for (const [sid, types] of m.entries()) {
      out[sid] = [...types.values()].sort((a, b) => a.name.localeCompare(b.name))
    }
    return out
  }, [campaignMaterials, materialsById, materialTypes, distributionRules])

  // Drafts: estado local por station_id. Hidrata a partir do server uma vez
  // ao carregar; mudanças subsequentes vivem aqui até saveAll/persist.
  const [drafts, setDrafts] = useState({})
  const [hydrated, setHydrated] = useState(false)

  useEffect(() => {
    if (hydrated || isLoading) return
    const next = {}
    for (const st of campaignStations) {
      const existing = pricingList.find(p => p.station_id === st.id)
      if (existing) {
        next[st.id] = {
          mode: existing.mode,
          consolidated: existing.consolidated_value ?? null,
          perType: Object.fromEntries(
            (existing.per_type ?? []).map(t => [t.type_id, t.unit_value])
          ),
        }
      } else {
        // Default sugerido: per_insertion se a estação tem materiais com tipo
        // definido (caso comum); consolidado caso contrário (estação "morta").
        const hasTypes = (typesInScope[st.id] ?? []).length > 0
        next[st.id] = {
          mode: hasTypes ? MODE_PER_INSERTION : MODE_CONSOLIDATED,
          consolidated: null,
          perType: {},
        }
      }
    }
    setDrafts(next)
    setHydrated(true)
  }, [hydrated, isLoading, pricingList, campaignStations, typesInScope])

  function updateDraft(stationId, patch) {
    setDrafts(prev => ({
      ...prev,
      [stationId]: { ...(prev[stationId] ?? {}), ...patch },
    }))
  }

  function setMode(stationId, mode) {
    setDrafts(prev => ({
      ...prev,
      [stationId]: {
        ...(prev[stationId] ?? {}),
        mode,
        // limpa o lado oposto pra evitar lixo no payload
        consolidated: mode === MODE_CONSOLIDATED ? (prev[stationId]?.consolidated ?? null) : null,
        perType: mode === MODE_PER_INSERTION ? (prev[stationId]?.perType ?? {}) : {},
      },
    }))
  }

  // ── Validação global pro parent ─────────────────────────────────────────
  const allValid = useMemo(() => {
    if (!hydrated || campaignStations.length === 0) return false
    return campaignStations.every(st => {
      const d = drafts[st.id]
      const types = typesInScope[st.id] ?? []
      return validateStation(d, types).ok
    })
  }, [hydrated, drafts, campaignStations, typesInScope])

  // ── Persistência em massa: chamada pelo wizard antes do Concluir ───────
  async function saveAll() {
    const errors = []
    // Persiste o fixed_cpm primeiro (uma chamada só) se mudou. Falha aqui não
    // bloqueia os per-station pricing — vamos coletar todos os erros e mostrar
    // no final.
    if (fixedCPM !== savedFixedCPMRef.current) {
      try {
        await updateFixedCPM.mutateAsync({ id: campaignId, value: fixedCPM })
        savedFixedCPMRef.current = fixedCPM
      } catch (e) {
        errors.push(`CPM fixo: ${e?.response?.data || 'erro de rede'}`)
      }
    }
    for (const st of campaignStations) {
      const d = drafts[st.id]
      const types = typesInScope[st.id] ?? []
      const v = validateStation(d, types)
      if (!v.ok) {
        errors.push(`${st.name}: ${v.reason}`)
        continue
      }
      try {
        if (d.mode === MODE_CONSOLIDATED) {
          await upsert.mutateAsync({
            campaignId, stationId: st.id,
            mode: MODE_CONSOLIDATED,
            consolidated_value: d.consolidated,
            per_type: null,
          })
        } else {
          await upsert.mutateAsync({
            campaignId, stationId: st.id,
            mode: MODE_PER_INSERTION,
            consolidated_value: null,
            per_type: types.map(t => ({
              type_id: t.id,
              unit_value: d.perType[t.id] ?? 0,
            })),
          })
        }
      } catch (e) {
        errors.push(`${st.name}: ${e?.response?.data || 'erro de rede'}`)
      }
    }
    if (errors.length > 0) {
      window.alert('Erros ao salvar valores:\n\n' + errors.join('\n'))
      return false
    }
    return true
  }

  useImperativeHandle(ref, () => ({
    saveAll,
    isValid: () => allValid,
  }))

  // ── Stats no topo ───────────────────────────────────────────────────────
  // Total previsto = soma de consolidados + soma de (unit_value × plays/dia
  // esperados). Como não temos plays direto aqui, mostramos só o total
  // CADASTRADO (consolidado + unitários somados crus). É um "valor base".
  const totalBase = useMemo(() => {
    let total = 0
    for (const st of campaignStations) {
      const d = drafts[st.id]
      if (!d) continue
      if (d.mode === MODE_CONSOLIDATED) {
        total += d.consolidated ?? 0
      } else {
        for (const t of (typesInScope[st.id] ?? [])) {
          total += d.perType[t.id] ?? 0
        }
      }
    }
    return total
  }, [drafts, campaignStations, typesInScope])

  const validCount = useMemo(() => {
    let c = 0
    for (const st of campaignStations) {
      const v = validateStation(drafts[st.id], typesInScope[st.id] ?? [])
      if (v.ok) c++
    }
    return c
  }, [drafts, campaignStations, typesInScope])

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 22 }}>
      {/* Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-end', gap: 16, flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6, maxWidth: 640 }}>
          <h2 style={{
            margin: 0, fontFamily: 'var(--font-heading)', fontWeight: 700,
            fontSize: 24, color: 'var(--c-text)', letterSpacing: '-0.01em',
          }}>
            Quanto vale cada emissora?
          </h2>
          <p style={{ margin: 0, color: 'var(--c-text-2)', fontSize: 13, lineHeight: 1.55 }}>
            Pra cada emissora, escolha entre <strong style={{ color: 'var(--c-text)' }}>valor consolidado</strong>{' '}
            (um pacote fechado) ou <strong style={{ color: 'var(--c-text)' }}>valor por inserção</strong>{' '}
            (preço unitário por tipo de material). Os totais de <em>/detections</em>{' '}
            saem daqui. O <strong style={{ color: 'var(--c-text)' }}>CPM</strong> é
            calculado dinamicamente (executado ÷ impactos × 1000) — ou pode ser
            travado com o campo <strong style={{ color: 'var(--c-text)' }}>CPM fixo</strong> ao lado.
          </p>
        </div>

        <SummaryStrip
          total={totalBase}
          validCount={validCount}
          totalCount={campaignStations.length}
          fixedCPM={fixedCPM}
          onFixedCPMChange={setFixedCPM}
        />
      </div>

      {/* Cards */}
      {!hydrated ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          {[1, 2, 3].map(i => (
            <div key={i} className="skeleton" style={{ height: 110, borderRadius: 'var(--radius-lg)' }} />
          ))}
        </div>
      ) : campaignStations.length === 0 ? (
        <EmptyStations />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          {campaignStations.map(st => (
            <StationPricingCard
              key={st.id}
              station={st}
              types={typesInScope[st.id] ?? []}
              draft={drafts[st.id]}
              onModeChange={mode => setMode(st.id, mode)}
              onConsolidatedChange={v => updateDraft(st.id, { consolidated: v })}
              onPerTypeChange={(typeId, v) =>
                updateDraft(st.id, {
                  perType: { ...(drafts[st.id]?.perType ?? {}), [typeId]: v },
                })}
              validation={validateStation(drafts[st.id], typesInScope[st.id] ?? [])}
            />
          ))}
        </div>
      )}
    </div>
  )
})

export default PricingStep

// ── Sub-components ──────────────────────────────────────────────────────────

function SummaryStrip({ total, validCount, totalCount, fixedCPM, onFixedCPMChange }) {
  const pct = totalCount > 0 ? Math.round((validCount / totalCount) * 100) : 0
  const allDone = validCount === totalCount && totalCount > 0
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 18,
      padding: '12px 18px',
      borderRadius: 'var(--radius-lg)',
      background: 'var(--c-surface)',
      border: '1px solid var(--c-border)',
      boxShadow: 'var(--shadow-sm)',
      minWidth: 420,
    }}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
        <span style={{
          fontSize: 9.5, fontWeight: 700, letterSpacing: '0.14em',
          color: 'var(--c-text-3)', textTransform: 'uppercase',
          fontFamily: 'var(--font-heading)',
        }}>
          Valor base
        </span>
        <span style={{
          fontFamily: 'var(--font-heading)', fontWeight: 700,
          fontSize: 18, color: 'var(--c-text)',
          fontVariantNumeric: 'tabular-nums',
        }}>
          {fmtBRL(total)}
        </span>
      </div>
      <div style={{ width: 1, alignSelf: 'stretch', background: 'var(--c-border)' }} />
      <FixedCPMField value={fixedCPM} onChange={onFixedCPMChange} />
      <div style={{ width: 1, alignSelf: 'stretch', background: 'var(--c-border)' }} />
      <div style={{ display: 'flex', flexDirection: 'column', gap: 2, flex: 1, minWidth: 130 }}>
        <span style={{
          fontSize: 9.5, fontWeight: 700, letterSpacing: '0.14em',
          color: 'var(--c-text-3)', textTransform: 'uppercase',
          fontFamily: 'var(--font-heading)',
        }}>
          Emissoras cadastradas
        </span>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 6 }}>
          <span style={{
            fontFamily: 'var(--font-heading)', fontWeight: 700,
            fontSize: 18, color: allDone ? 'var(--c-success)' : 'var(--c-text)',
            fontVariantNumeric: 'tabular-nums',
          }}>
            {validCount}/{totalCount}
          </span>
          <span style={{ fontSize: 11, color: 'var(--c-text-3)' }}>{pct}%</span>
        </div>
        <div style={{
          height: 3, marginTop: 4, borderRadius: 'var(--radius-full)',
          background: 'var(--c-surface-2)', overflow: 'hidden',
        }}>
          <div style={{
            height: '100%',
            width: `${pct}%`,
            background: allDone ? 'var(--c-success)' : 'var(--c-action)',
            transition: 'width 240ms cubic-bezier(0.16,1,0.3,1)',
          }} />
        </div>
      </div>
    </div>
  )
}

// CPM fixo opcional da campanha. Quando vazio, /campaigns, /insights e o
// dashboard calculam o CPM dinamicamente ((executado + bonificado) / impactos ×
// 1000 — o valor de tabela da mídia entregue, não só o que foi pago). Quando
// preenchido aqui, esse valor é exibido em todos esses lugares — útil pra
// campanhas com CPM pré-acordado que o cálculo derivado distorce.
function FixedCPMField({ value, onChange }) {
  const [focused, setFocused] = useState(false)
  const [str, setStr] = useState(() => value != null ? value.toFixed(2).replace('.', ',') : '')

  useEffect(() => {
    if (focused) return
    setStr(value != null ? value.toFixed(2).replace('.', ',') : '')
  }, [value, focused])

  const isSet = value != null
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 140 }}>
      <span
        title="Quando preenchido, sobrescreve o CPM exibido em /campaigns, /insights e dashboard. Deixe vazio para usar o cálculo dinâmico ((executado + bonificado) ÷ impactos × 1000)."
        style={{
          fontSize: 9.5, fontWeight: 700, letterSpacing: '0.14em',
          color: 'var(--c-text-3)', textTransform: 'uppercase',
          fontFamily: 'var(--font-heading)',
          display: 'inline-flex', alignItems: 'center', gap: 4,
          cursor: 'help',
        }}>
        CPM fixo
        <span style={{
          fontSize: 8.5, fontWeight: 600, letterSpacing: '0.08em',
          color: isSet ? 'var(--c-action)' : 'var(--c-text-3)',
          opacity: isSet ? 1 : 0.7,
        }}>
          {isSet ? '· ativo' : '· opcional'}
        </span>
      </span>
      <div style={{
        position: 'relative',
        display: 'flex', alignItems: 'center',
        width: 140,
        borderRadius: 'var(--radius-md)',
        border: `1px solid ${focused ? 'var(--c-action)' : 'var(--c-border)'}`,
        background: 'var(--c-surface)',
        transition: 'border-color 140ms, box-shadow 140ms',
        boxShadow: focused ? '0 0 0 3px var(--c-action-light)' : 'none',
      }}>
        <span style={{
          padding: '0 4px 0 10px',
          fontSize: 11, fontWeight: 600,
          color: focused ? 'var(--c-action)' : 'var(--c-text-3)',
          fontFamily: 'var(--font-heading)',
          transition: 'color 140ms',
        }}>
          R$
        </span>
        <input
          value={str}
          onChange={e => {
            const v = e.target.value
            setStr(v)
            const parsed = parseCurrency(v)
            onChange(parsed)
          }}
          onFocus={() => setFocused(true)}
          onBlur={() => {
            setFocused(false)
            const parsed = parseCurrency(str)
            if (parsed != null) setStr(parsed.toFixed(2).replace('.', ','))
            else setStr('')
          }}
          placeholder="usa cálc. dinâmico"
          inputMode="decimal"
          style={{
            flex: 1, width: '100%',
            padding: '6px 8px 6px 0',
            border: 0, background: 'transparent',
            fontSize: 13, fontWeight: 600,
            color: 'var(--c-text)',
            fontFamily: 'var(--font-heading)',
            fontVariantNumeric: 'tabular-nums',
            outline: 'none', textAlign: 'right',
          }}
        />
      </div>
    </div>
  )
}

function EmptyStations() {
  return (
    <div style={{
      padding: '40px 24px', textAlign: 'center',
      background: 'var(--c-bg)', border: '1px dashed var(--c-border)',
      borderRadius: 'var(--radius-lg)', color: 'var(--c-text-2)',
    }}>
      <p style={{ margin: 0, fontSize: 14 }}>
        Volte ao passo 2 e selecione pelo menos uma emissora antes de cadastrar valores.
      </p>
    </div>
  )
}

function StationPricingCard({
  station, types, draft, onModeChange, onConsolidatedChange, onPerTypeChange, validation,
}) {
  const hasTypes = types.length > 0
  const ok = validation.ok
  return (
    <article style={{
      background: 'var(--c-surface)',
      border: `1px solid ${ok ? 'var(--c-border)' : 'var(--c-action-border)'}`,
      borderRadius: 'var(--radius-xl)',
      boxShadow: 'var(--shadow-sm)',
      overflow: 'hidden',
      transition: 'border-color 160ms',
    }}>
      {/* Header da emissora */}
      <header style={{
        display: 'flex', alignItems: 'center', gap: 14,
        padding: '14px 18px',
        borderBottom: '1px solid var(--c-border)',
      }}>
        <StationAvatar station={station} size={40} />
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{
            fontFamily: 'var(--font-heading)', fontWeight: 700,
            fontSize: 15, color: 'var(--c-text)',
            whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
          }}>
            {station.name}
          </div>
          <div style={{ fontSize: 11.5, color: 'var(--c-text-3)', marginTop: 2 }}>
            {station.band} {station.frequency_mhz ?? ''}
            {station.city ? ` · ${station.city}${station.state ? '/' + station.state : ''}` : ''}
            {station.pmm != null && (
              <> · <strong style={{ color: 'var(--c-text-2)' }}>PMM {Math.round(station.pmm)}</strong></>
            )}
          </div>
        </div>
        <ValidationBadge ok={ok} reason={validation.reason} />
      </header>

      {/* Mode toggle */}
      <div style={{ padding: '14px 18px 0', display: 'flex', gap: 8 }}>
        <ModeChip
          active={draft?.mode === MODE_CONSOLIDATED}
          onClick={() => onModeChange(MODE_CONSOLIDATED)}
          icon={<IconBox />}
          label="Valor consolidado"
          hint="Pacote fechado pra emissora inteira"
        />
        <ModeChip
          active={draft?.mode === MODE_PER_INSERTION}
          onClick={() => onModeChange(MODE_PER_INSERTION)}
          icon={<IconBars />}
          label="Por inserção"
          hint={hasTypes
            ? `Valor unitário por tipo (${types.length})`
            : 'Sem tipos linkados nessa emissora'}
          disabled={!hasTypes}
        />
      </div>

      {/* Body — varia por modo */}
      <div style={{ padding: '12px 18px 18px' }}>
        {draft?.mode === MODE_CONSOLIDATED ? (
          <ConsolidatedField
            value={draft?.consolidated ?? null}
            onChange={onConsolidatedChange}
          />
        ) : draft?.mode === MODE_PER_INSERTION ? (
          <PerTypeFields
            types={types}
            values={draft?.perType ?? {}}
            onChange={onPerTypeChange}
          />
        ) : null}
      </div>
    </article>
  )
}

function ValidationBadge({ ok, reason }) {
  if (ok) {
    return (
      <span style={{
        display: 'inline-flex', alignItems: 'center', gap: 5,
        padding: '4px 10px', borderRadius: 'var(--radius-full)',
        background: 'rgba(22, 163, 74, 0.10)', color: 'var(--c-success)',
        fontSize: 10.5, fontWeight: 700, fontFamily: 'var(--font-heading)',
        letterSpacing: '0.04em', textTransform: 'uppercase',
      }} title="Pronto pra salvar">
        <svg width="10" height="10" viewBox="0 0 16 16" fill="none">
          <path d="M3 8.5l3 3 7-7" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
        OK
      </span>
    )
  }
  return (
    <span style={{
      display: 'inline-flex', alignItems: 'center', gap: 5,
      padding: '4px 10px', borderRadius: 'var(--radius-full)',
      background: 'var(--c-action-light)', color: 'var(--c-action)',
      fontSize: 10.5, fontWeight: 700, fontFamily: 'var(--font-heading)',
      letterSpacing: '0.04em', textTransform: 'uppercase',
    }} title={reason}>
      Pendente
    </span>
  )
}

function ModeChip({ active, onClick, icon, label, hint, disabled }) {
  const [hover, setHover] = useState(false)
  const bg = disabled ? 'var(--c-surface-2)'
    : active ? 'var(--c-action-light)'
    : hover ? 'var(--c-bg)'
    : 'var(--c-surface)'
  const border = disabled ? 'var(--c-border)'
    : active ? 'var(--c-action)'
    : hover ? 'var(--c-text-3)'
    : 'var(--c-border)'
  const color = disabled ? 'var(--c-text-3)' : active ? 'var(--c-action)' : 'var(--c-text)'
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      style={{
        flex: 1,
        display: 'flex', alignItems: 'center', gap: 10,
        padding: '10px 14px',
        borderRadius: 'var(--radius-md)',
        border: `1.5px solid ${border}`,
        background: bg,
        color,
        textAlign: 'left',
        cursor: disabled ? 'not-allowed' : 'pointer',
        opacity: disabled ? 0.65 : 1,
        transition: 'all 140ms cubic-bezier(0.16,1,0.3,1)',
        fontFamily: 'inherit',
      }}
    >
      <span style={{
        display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
        width: 28, height: 28, borderRadius: 'var(--radius-md)',
        background: active ? 'var(--c-action)' : 'var(--c-surface-2)',
        color: active ? '#fff' : 'var(--c-text-3)',
        flexShrink: 0,
        transition: 'all 140ms',
      }}>
        {icon}
      </span>
      <div style={{ minWidth: 0 }}>
        <div style={{
          fontSize: 12.5, fontWeight: 700, fontFamily: 'var(--font-heading)',
          letterSpacing: '0.01em',
        }}>
          {label}
        </div>
        <div style={{ fontSize: 10.5, color: 'var(--c-text-3)', marginTop: 1, fontWeight: 500 }}>
          {hint}
        </div>
      </div>
    </button>
  )
}

function ConsolidatedField({ value, onChange }) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 12,
      padding: '14px 16px',
      borderRadius: 'var(--radius-md)',
      background: 'var(--c-bg)',
      border: '1px solid var(--c-border)',
    }}>
      <div style={{ flex: 1 }}>
        <Label>Valor total da campanha pra essa emissora</Label>
        <Hint>Toda inserção que rodar conta dentro desse valor — bonificação nessa emissora não acrescenta valor, só impactos (e portanto baixa o CPM).</Hint>
      </div>
      <CurrencyInput value={value} onChange={onChange} placeholder="R$ 0,00" />
    </div>
  )
}

function PerTypeFields({ types, values, onChange }) {
  if (types.length === 0) {
    return (
      <div style={{
        padding: '14px 16px',
        borderRadius: 'var(--radius-md)',
        background: 'var(--c-action-light)',
        border: '1px solid var(--c-action-border)',
        fontSize: 12.5, color: 'var(--c-action)',
        fontFamily: 'var(--font-heading)', fontWeight: 600,
      }}>
        Essa emissora não tem materiais com tipo definido. Use{' '}
        <strong>valor consolidado</strong>, ou volte ao passo 3 pra cadastrar
        materiais.
      </div>
    )
  }
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {types.map(t => (
        <div key={t.id} style={{
          display: 'flex', alignItems: 'center', gap: 12,
          padding: '10px 14px',
          borderRadius: 'var(--radius-md)',
          background: 'var(--c-bg)',
          border: '1px solid var(--c-border)',
        }}>
          <span style={{
            width: 4, height: 24, borderRadius: 2,
            background: t.color ?? 'var(--c-text-3)', flexShrink: 0,
          }} />
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{
              fontFamily: 'var(--font-heading)', fontWeight: 600,
              fontSize: 13, color: 'var(--c-text)',
            }}>
              {t.name}
            </div>
            <div style={{ fontSize: 10.5, color: 'var(--c-text-3)', marginTop: 1 }}>
              Valor por inserção
            </div>
          </div>
          <CurrencyInput
            value={values[t.id] ?? null}
            onChange={v => onChange(t.id, v)}
            placeholder="R$ 0,00"
            compact
          />
        </div>
      ))}
    </div>
  )
}

function CurrencyInput({ value, onChange, placeholder, compact }) {
  const [focused, setFocused] = useState(false)
  // Mantém uma string local pra permitir digitação livre (com vírgula).
  const [str, setStr] = useState(() => value != null ? value.toFixed(2).replace('.', ',') : '')

  useEffect(() => {
    if (focused) return
    setStr(value != null ? value.toFixed(2).replace('.', ',') : '')
  }, [value, focused])

  return (
    <div style={{
      position: 'relative',
      display: 'flex', alignItems: 'center',
      width: compact ? 150 : 200,
      borderRadius: 'var(--radius-md)',
      border: `1px solid ${focused ? 'var(--c-action)' : 'var(--c-border)'}`,
      background: 'var(--c-surface)',
      transition: 'border-color 140ms, box-shadow 140ms',
      boxShadow: focused ? '0 0 0 3px var(--c-action-light)' : 'none',
    }}>
      <span style={{
        padding: '0 6px 0 12px',
        fontSize: 12, fontWeight: 600,
        color: focused ? 'var(--c-action)' : 'var(--c-text-3)',
        fontFamily: 'var(--font-heading)',
        transition: 'color 140ms',
      }}>
        R$
      </span>
      <input
        value={str}
        onChange={e => {
          const v = e.target.value
          setStr(v)
          onChange(parseCurrency(v))
        }}
        onFocus={() => setFocused(true)}
        onBlur={() => {
          setFocused(false)
          const parsed = parseCurrency(str)
          if (parsed != null) setStr(parsed.toFixed(2).replace('.', ','))
        }}
        placeholder={placeholder}
        inputMode="decimal"
        style={{
          flex: 1, width: '100%',
          padding: '9px 10px 9px 0',
          border: 0, background: 'transparent',
          fontSize: 13, fontWeight: 600,
          color: 'var(--c-text)',
          fontFamily: 'var(--font-heading)',
          fontVariantNumeric: 'tabular-nums',
          outline: 'none', textAlign: 'right',
        }}
      />
    </div>
  )
}

function Label({ children }) {
  return (
    <div style={{
      fontSize: 11, fontWeight: 700, letterSpacing: '0.06em',
      color: 'var(--c-text-2)', textTransform: 'uppercase',
      fontFamily: 'var(--font-heading)',
    }}>
      {children}
    </div>
  )
}

function Hint({ children }) {
  return (
    <div style={{ fontSize: 11, color: 'var(--c-text-3)', marginTop: 3, lineHeight: 1.4 }}>
      {children}
    </div>
  )
}

function IconBox() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <rect x="2.5" y="3" width="11" height="10" rx="1.2" />
      <path d="M2.5 6.5h11" />
    </svg>
  )
}

function IconBars() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <path d="M3 12V6" />
      <path d="M8 12V3" />
      <path d="M13 12V8" />
    </svg>
  )
}
