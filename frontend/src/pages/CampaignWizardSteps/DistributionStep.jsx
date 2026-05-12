import { useState, useMemo } from 'react'
import {
  useDistributionRules, useDistributionOverrides, useDailySummary,
  useCreateDistributionRule, useUpdateDistributionRule, useDeleteDistributionRule,
  useUpsertOverride, useDeleteOverride,
  useMaterialTypes,
} from '../../api/hooks'
import DistributionGrid from '../../components/DistributionGrid'
import MonthNavigator from '../../components/MonthNavigator'
import RuleSidePanel from '../../components/RuleSidePanel'
import OverridePopover from '../../components/OverridePopover'
import { useConfirm } from '../../components/ConfirmModal'

/**
 * Step 4: distribution rules + grid + override.
 *
 * Props:
 *  - campaignId: uuid
 *  - campaignStart: ISO
 *  - campaignEnd: ISO
 *  - campaignMaterials: Array<{material_id, target_stations[], ...}>
 *  - materialsById: Record<uuid, Material> (hydrated by parent — provides title + type_id)
 *  - allStations: Array<station>
 */
export default function DistributionStep({
  campaignId, campaignStart, campaignEnd,
  campaignMaterials, materialsById = {}, allStations,
}) {
  const cStart = new Date(campaignStart)
  const cEnd = new Date(campaignEnd)
  const [month, setMonth] = useState(() => new Date(cStart.getFullYear(), cStart.getMonth(), 1))

  const fromISO = new Date(month.getFullYear(), month.getMonth(), 1).toISOString().slice(0, 10)
  const toISO   = new Date(month.getFullYear(), month.getMonth() + 1, 0).toISOString().slice(0, 10)

  const { data: rules = [] } = useDistributionRules(campaignId)
  const { data: overrides = [] } = useDistributionOverrides(campaignId, fromISO, toISO)
  const { data: summary = [] } = useDailySummary(campaignId, fromISO, toISO)
  const { data: materialTypes = [] } = useMaterialTypes()

  const createRule = useCreateDistributionRule()
  const updateRule = useUpdateDistributionRule()
  const deleteRule = useDeleteDistributionRule()
  const upsertOverride = useUpsertOverride()
  const deleteOverride = useDeleteOverride()
  const confirm = useConfirm()

  const [ruleEditOpen, setRuleEditOpen] = useState(false)
  const [editingRule, setEditingRule] = useState(null)
  const [popoverAnchor, setPopoverAnchor] = useState(null)
  const [popoverContext, setPopoverContext] = useState(null) // { stationId, materialId, date }

  const typeColorById = Object.fromEntries(materialTypes.map(t => [t.id, t.color]))

  // Build "rows": one per (station, material) combination
  const rows = useMemo(() => {
    const r = []
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      if (!mat) continue
      for (const sid of cm.target_stations) {
        const matching = rules.filter(rule =>
          rule.material_id === cm.material_id && rule.station_ids.includes(sid))
        const first = matching[0]
        r.push({
          stationId: sid,
          materialId: cm.material_id,
          materialTitle: mat.title,
          typeColor: typeColorById[mat.type_id] ?? '#94a3b8',
          ruleSummary: first
            ? `${first.plays_per_day}×/dia ${first.time_start}–${first.time_end}`
            : null,
          extraRules: Math.max(0, matching.length - 1),
        })
      }
    }
    return r
  }, [campaignMaterials, rules, materialsById, typeColorById])

  // Build cellData map from summary + override marker
  const cellData = useMemo(() => {
    const m = new Map()
    for (const s of summary) {
      const key = `${s.station_id}|${s.material_id}|${s.for_date.slice(0, 10)}`
      const hasOverride = overrides.some(o =>
        o.station_id === s.station_id && o.material_id === s.material_id &&
        o.for_date.slice(0, 10) === s.for_date.slice(0, 10))
      m.set(key, { ...s, hasOverride })
    }
    return m
  }, [summary, overrides])

  function openRuleEditor(existing = null) {
    setEditingRule(existing)
    setRuleEditOpen(true)
  }

  async function submitRule(payload) {
    try {
      if (editingRule) {
        await updateRule.mutateAsync({ campaignId, ruleId: editingRule.id, ...payload })
      } else {
        await createRule.mutateAsync({ campaignId, ...payload })
      }
      setRuleEditOpen(false)
    } catch {
      window.alert('Erro ao salvar regra. Verifique os campos e tente novamente.')
    }
  }

  async function handleDeleteRule() {
    if (!editingRule) return
    const ok = await confirm('Excluir esta regra?')
    if (!ok) return
    await deleteRule.mutateAsync({ campaignId, ruleId: editingRule.id })
    setRuleEditOpen(false)
  }

  function handleCellClick(stationId, materialId, dateISO, rect) {
    setPopoverAnchor(rect)
    setPopoverContext({ stationId, materialId, date: dateISO })
  }

  const ctx = popoverContext
  const cellKey = ctx ? `${ctx.stationId}|${ctx.materialId}|${ctx.date}` : null
  const cellInfo = cellKey ? cellData.get(cellKey) : null
  const matchingOverride = ctx ? overrides.find(o =>
    o.station_id === ctx.stationId && o.material_id === ctx.materialId &&
    o.for_date.slice(0, 10) === ctx.date) : null

  // Pre-compute lists for the RuleSidePanel
  const ruleEditorMaterials = useMemo(() =>
    campaignMaterials.map(cm => {
      const mat = materialsById[cm.material_id]
      return {
        id: cm.material_id,
        title: mat?.title ?? '(material)',
        type_color: typeColorById[mat?.type_id],
      }
    }), [campaignMaterials, materialsById, typeColorById])

  const ruleEditorStations = useMemo(() => {
    const stationSet = new Set()
    for (const cm of campaignMaterials) {
      for (const sid of cm.target_stations) stationSet.add(sid)
    }
    return [...stationSet].map(id => allStations.find(s => s.id === id)).filter(Boolean)
  }, [campaignMaterials, allStations])

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 22 }}>
      {/* Section title + controls */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-end', gap: 16, flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6, maxWidth: 640 }}>
          <h2 style={{
            margin: 0, fontFamily: 'var(--font-heading)', fontWeight: 700,
            fontSize: 24, color: 'var(--c-text)', letterSpacing: '-0.01em',
          }}>
            Quando e quantas vezes vai tocar?
          </h2>
          <p style={{ margin: 0, color: 'var(--c-text-2)', fontSize: 13, lineHeight: 1.55 }}>
            Crie regras que definem <strong style={{ color: 'var(--c-text)' }}>quantas vezes</strong> cada material
            toca por dia, em quais emissoras, faixa horária e período. Clique nas células do calendário
            pra criar exceções pontuais.
          </p>
        </div>
        <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
          {(rules.length > 0 || overrides.length > 0) && (
            <div style={{
              display: 'flex', alignItems: 'center', gap: 8,
              padding: '6px 12px', borderRadius: 'var(--radius-full)',
              background: 'var(--c-bg)', border: '1px solid var(--c-border)',
              fontSize: 11, fontWeight: 600, color: 'var(--c-text-2)',
              fontFamily: 'var(--font-heading)',
            }}>
              <span><strong style={{ color: 'var(--c-text)' }}>{rules.length}</strong> regra{rules.length !== 1 ? 's' : ''}</span>
              <span style={{ color: 'var(--c-text-3)' }}>·</span>
              <span><strong style={{ color: 'var(--c-text)' }}>{overrides.length}</strong> override{overrides.length !== 1 ? 's' : ''}</span>
            </div>
          )}
          <MonthNavigator
            value={month}
            onChange={setMonth}
            minDate={cStart}
            maxDate={cEnd}
          />
          <button
            onClick={() => openRuleEditor(null)}
            style={{
              padding: '9px 16px', borderRadius: 'var(--radius-md)',
              background: 'var(--c-action)', color: '#fff', border: 0,
              cursor: 'pointer', fontSize: 12, fontWeight: 700,
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
            Nova regra
          </button>
        </div>
      </div>

      {rows.length === 0 ? (
        <EmptyDistributionState onAddRule={() => openRuleEditor(null)} />
      ) : (
        <DistributionGrid
          mode="edit"
          month={month}
          campaignStart={campaignStart}
          campaignEnd={campaignEnd}
          stations={allStations}
          rows={rows}
          cellData={cellData}
          onCellClick={handleCellClick}
          onCellIncrement={(stationId, materialId, dateISO, currentValue) => {
            upsertOverride.mutate({
              campaignId,
              material_id: materialId,
              station_id: stationId,
              for_date: dateISO,
              plays_expected: currentValue + 1,
            })
          }}
          onCellDecrement={(stationId, materialId, dateISO, currentValue) => {
            upsertOverride.mutate({
              campaignId,
              material_id: materialId,
              station_id: stationId,
              for_date: dateISO,
              plays_expected: Math.max(0, currentValue - 1),
            })
          }}
        />
      )}

      <RuleSidePanel
        open={ruleEditOpen}
        onClose={() => setRuleEditOpen(false)}
        onSubmit={submitRule}
        onDelete={editingRule ? handleDeleteRule : undefined}
        mode={editingRule ? 'edit' : 'create'}
        initial={editingRule}
        materials={ruleEditorMaterials}
        stations={ruleEditorStations}
        campaignStart={campaignStart}
        campaignEnd={campaignEnd}
        submitting={createRule.isPending || updateRule.isPending}
      />

      <OverridePopover
        open={!!popoverAnchor && !!ctx}
        anchorRect={popoverAnchor}
        onClose={() => { setPopoverAnchor(null); setPopoverContext(null) }}
        onApply={async (newValue) => {
          await upsertOverride.mutateAsync({
            campaignId,
            material_id: ctx.materialId,
            station_id: ctx.stationId,
            for_date: ctx.date,
            plays_expected: newValue,
          })
          setPopoverAnchor(null); setPopoverContext(null)
        }}
        onRevert={async () => {
          await deleteOverride.mutateAsync({
            campaignId,
            material_id: ctx.materialId,
            station_id: ctx.stationId,
            for_date: ctx.date,
          })
          setPopoverAnchor(null); setPopoverContext(null)
        }}
        currentRuleValue={cellInfo?.expected ?? 0}
        currentOverrideValue={matchingOverride?.plays_expected ?? null}
        materialTitle={rows.find(r => r.materialId === ctx?.materialId)?.materialTitle ?? '—'}
        stationName={allStations.find(s => s.id === ctx?.stationId)?.name ?? '—'}
        date={ctx?.date}
      />
    </div>
  )
}

function EmptyDistributionState({ onAddRule }) {
  // Ghost preview of what a populated grid looks like: a faded mini-calendar
  // hinting at days × stations cells.
  return (
    <div style={{
      position: 'relative',
      padding: '48px 32px',
      background: 'var(--c-bg)',
      border: '1px dashed var(--c-border)',
      borderRadius: 'var(--radius-xl)',
      overflow: 'hidden',
      minHeight: 320,
    }}>
      {/* Ghost mini-grid */}
      <div style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(14, 1fr)',
        gap: 4, opacity: 0.28,
        pointerEvents: 'none',
      }}>
        {Array.from({ length: 14 * 5 }).map((_, i) => {
          const filled = [3, 5, 9, 11, 17, 19, 21, 33, 37, 41, 47, 51, 55, 59, 63, 65].includes(i)
          return (
            <div key={i} style={{
              aspectRatio: '1 / 1',
              background: filled ? 'var(--c-action)' : 'var(--c-surface-2)',
              borderRadius: 4,
            }} />
          )
        })}
      </div>

      {/* Overlay CTA */}
      <div style={{
        position: 'absolute', inset: 0,
        display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
        gap: 8,
        background: 'linear-gradient(to bottom, rgba(248,250,252,0.5), rgba(248,250,252,0.98))',
        padding: 24,
      }}>
        <div style={{
          width: 56, height: 56, borderRadius: '50%',
          background: 'var(--c-surface)', boxShadow: 'var(--shadow-md)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          color: 'var(--c-action)', marginBottom: 6,
        }}>
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
            <rect x="3" y="5" width="18" height="16" rx="2" />
            <path d="M16 3v4M8 3v4M3 10h18" />
          </svg>
        </div>
        <h3 style={{
          margin: 0, fontFamily: 'var(--font-heading)', fontWeight: 700,
          fontSize: 16, color: 'var(--c-text)',
        }}>
          Crie a primeira regra de distribuição
        </h3>
        <p style={{
          margin: 0, color: 'var(--c-text-2)', fontSize: 13,
          textAlign: 'center', maxWidth: 420, lineHeight: 1.55,
        }}>
          Uma regra diz quantas vezes um material toca por dia, em quais
          emissoras, em qual faixa horária e durante qual período da campanha.
        </p>
        <button
          onClick={onAddRule}
          style={{
            marginTop: 10, padding: '10px 18px', borderRadius: 'var(--radius-md)',
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
          Adicionar primeira regra
        </button>
      </div>
    </div>
  )
}
