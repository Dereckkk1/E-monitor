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
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '0 0 12px' }}>
        <h3 style={{ margin: 0, fontSize: 16 }}>Distribua os materiais</h3>
        <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
          <span style={{ fontSize: 11, color: '#64748b' }}>
            {rules.length} regra{rules.length !== 1 ? 's' : ''}, {overrides.length} override{overrides.length !== 1 ? 's' : ''}
          </span>
          <MonthNavigator
            value={month}
            onChange={setMonth}
            minDate={cStart}
            maxDate={cEnd}
          />
          <button onClick={() => openRuleEditor(null)} className="btn btn-primary btn-sm">
            + Regra
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
  return (
    <div style={{
      padding: 48, textAlign: 'center', background: '#fafbfc',
      border: '1px dashed #e2e8f0', borderRadius: 12,
    }}>
      <div style={{ fontSize: 40, color: '#cbd5e1', marginBottom: 8 }}>▦</div>
      <h4 style={{ margin: '0 0 6px', color: '#0f172a' }}>Comece criando uma regra</h4>
      <p style={{ margin: '0 0 16px', color: '#64748b', fontSize: 13 }}>
        Uma regra define quantas vezes um material toca por dia, em quais emissoras, faixa horária e período.
      </p>
      <button onClick={onAddRule} className="btn btn-primary btn-sm">+ Adicionar primeira regra</button>
    </div>
  )
}
